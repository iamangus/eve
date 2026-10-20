package io

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/emersion/go-ical"
	"github.com/iamangus/eve/internal/events"
	"github.com/teambition/rrule-go"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// CalDAVConfig configures the calendar channel. A single CalDAV collection is
// used for Eve's calendar. Empty URL disables the channel.
type CalDAVConfig struct {
	URL          string
	Username     string
	Password     string
	CalendarPath string
	ReminderLead time.Duration
}

func (c CalDAVConfig) Enabled() bool {
	return c.URL != ""
}

func (c CalDAVConfig) calendarURL() string {
	base := strings.TrimSuffix(c.URL, "/") + "/"
	if c.CalendarPath != "" {
		base += strings.TrimPrefix(c.CalendarPath, "/")
	}
	return strings.TrimRight(base, "/") + "/"
}

// CalEvent is a parsed iCalendar event as Eve understands it.
type CalEvent struct {
	UID          string
	Summary      string
	Start        time.Time
	End          time.Time
	RRule        string
	Resource     string `json:"-"`
	ETag         string `json:"-"`
	RecurrenceID string `json:"recurrence_id,omitempty"`
	raw          *ical.Calendar
}

// CalStore is a thin CalDAV client speaking only what we need: fetch the
// whole collection, put an event, delete an event. All bodies are .ics
// text/calendar payloads; auth is HTTP Basic.
type CalStore struct {
	cfg CalDAVConfig
	hc  *http.Client
}

func NewCalStore(cfg CalDAVConfig) *CalStore {
	return &CalStore{cfg: cfg, hc: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
			return fmt.Errorf("calendar redirect rejected")
		}
		return nil
	}}}
}

// List fetches the calendar collection and parses all VEVENTs.
func (c *CalStore) List(ctx context.Context) ([]CalEvent, error) {
	now := time.Now()
	return c.ListRange(ctx, now.AddDate(0, 0, -1), now.AddDate(0, 0, 60))
}

func (c *CalStore) auth(req *http.Request) {
	if c.cfg.Username != "" {
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	}
}

// Put writes (or overwrites) a VEVENT. A new event carries no UID and gets a
// fresh one; updating an existing event reuses its UID and the calendar
// resource path /uid.ics.
func (c *CalStore) Put(ctx context.Context, ev CalEvent) error {
	resource := c.cfg.calendarURL()
	if ev.UID == "" {
		return fmt.Errorf("event UID is required")
	}
	resource += url.PathEscape(ev.UID) + ".ics"
	if ev.Resource != "" {
		resource = ev.Resource
	} else if c.cfg.CalendarPath == "" {
		_, collection, err := c.calendarClient(ctx)
		if err != nil {
			return err
		}
		base, err := c.resourceURL(collection)
		if err != nil {
			return err
		}
		resource = strings.TrimRight(base, "/") + "/" + url.PathEscape(ev.UID) + ".ics"
	}
	if ev.Start.IsZero() || !ev.End.After(ev.Start) {
		return fmt.Errorf("event end must be after start")
	}
	data, err := encodeCalendar(ev)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, resource, strings.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
	if ev.ETag != "" {
		req.Header.Set("If-Match", strconv.Quote(ev.ETag))
	} else {
		req.Header.Set("If-None-Match", "*")
	}
	c.auth(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return fmt.Errorf("caldav put %s: %s", resp.Status, body)
	}
	return nil
}

// Delete removes the calendar resource for the event UID.
func (c *CalStore) Delete(ctx context.Context, uid string) error {
	ev, err := c.Find(ctx, uid)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, ev.Resource, nil)
	if err != nil {
		return err
	}
	c.auth(req)
	if ev.ETag == "" {
		return fmt.Errorf("calendar resource has no ETag; refusing unsafe delete")
	}
	req.Header.Set("If-Match", strconv.Quote(ev.ETag))
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return fmt.Errorf("caldav delete %s: %s", resp.Status, body)
	}
	return nil
}

// ICS renders the VEVENT as an iCalendar payload.
func (ev CalEvent) ICS() string {
	text, _ := encodeCalendar(ev)
	return text
}

// --- minimal iCalendar parsing ---------------------------------------------

// parseICalendar extracts VEVENTs from an .ics payload. Recurring events are
// expanded only for the quiet-hours pattern (FREQ=DAILY with DURATION via the
// event span): for the next 60 days each occurrence becomes a concrete event.
func parseICalendar(data string) []CalEvent {
	cal, err := ical.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		return nil
	}
	now := time.Now()
	out, _ := calendarEvents(cal, now.AddDate(0, 0, -1), now.AddDate(0, 0, 60))
	return out
}

// parseICalTime handles the two common DTSTART forms: UTC (20060102T150405Z)
// and date-only. Zone-less values are treated as UTC.
func parseICalTime(val, params string) time.Time {
	cal, err := ical.NewDecoder(strings.NewReader("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nDTSTART" + params + ":" + val + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")).Decode()
	if err != nil || len(cal.Children) == 0 {
		return time.Time{}
	}
	t, _ := cal.Children[0].Props.DateTime("DTSTART", time.UTC)
	return t
}

// expandRecurring materializes DAILY recurring events (quiet hours) into
// concrete occurrences over the next 60 days.
func expandRecurring(evs []CalEvent) []CalEvent {
	var out []CalEvent
	for _, ev := range evs {
		if ev.RRule == "" {
			out = append(out, ev)
			continue
		}
		dur := ev.End.Sub(ev.Start)
		if dur <= 0 {
			dur = time.Hour
		}
		option, err := rrule.StrToROption(ev.RRule)
		if err != nil {
			continue
		}
		option.Dtstart = ev.Start
		rule, err := rrule.NewRRule(*option)
		if err != nil {
			continue
		}
		for _, start := range rule.Between(time.Now().AddDate(0, 0, -1), time.Now().AddDate(0, 0, 60), true) {
			o := ev
			o.RRule = ""
			o.Start = start
			o.End = o.Start.Add(dur)
			out = append(out, o)
		}
	}
	return out
}

// upcomingReminder finds the next event that starts within lead from now.
func upcomingReminder(evs []CalEvent, lead time.Duration) (CalEvent, bool) {
	now := time.Now().UTC()
	var best CalEvent
	found := false
	for _, ev := range evs {
		if !ev.Start.After(now) {
			continue
		}
		if ev.Start.After(now.Add(lead)) {
			continue
		}
		if !found || ev.Start.Before(best.Start) {
			best = ev
			found = true
		}
	}
	return best, found
}

// freeBusy returns the busy intervals overlapping [from, to].
func freeBusy(evs []CalEvent, from, to time.Time) []map[string]string {
	out := make([]map[string]string, 0)
	for _, ev := range evs {
		if ev.End.Before(from) || ev.Start.After(to) {
			continue
		}
		out = append(out, map[string]string{
			"summary": ev.Summary,
			"start":   ev.Start.Format(time.RFC3339),
			"end":     ev.End.Format(time.RFC3339),
		})
	}
	return out
}

// --- MCP calendar tools ----------------------------------------------------

// addCalendarTools registers the calendar-management tools. They are no-ops
// (error) when the calendar is not configured.
func (m *MCP) addCalendarTools(s *server.MCPServer) {
	if m.mgr.Cal == nil {
		return
	}
	get := mcp.NewTool("get_calendar",
		mcp.WithDescription("List events on Eve's calendar, optionally within a time range."),
		mcp.WithString("from", mcp.Description("Start of range (RFC3339). Defaults to now.")),
		mcp.WithString("to", mcp.Description("End of range (RFC3339). Defaults to now+14 days.")),
	)
	s.AddTool(get, m.handleGetCalendar)

	create := mcp.NewTool("create_event",
		mcp.WithDescription("Create an event on Eve's calendar."),
		mcp.WithString("summary", mcp.Description("Event title.")),
		mcp.WithString("start", mcp.Description("Start time (RFC3339).")),
		mcp.WithString("end", mcp.Description("End time (RFC3339).")),
		mcp.WithString("rrule", mcp.Description("Optional RRULE (e.g. 'FREQ=WEEKLY;BYDAY=MO').")),
	)
	s.AddTool(create, m.handleCreateEvent)

	update := mcp.NewTool("update_event",
		mcp.WithDescription("Update an existing event on Eve's calendar by UID."),
		mcp.WithString("uid", mcp.Description("The UID of the event to update.")),
		mcp.WithString("summary", mcp.Description("New title.")),
		mcp.WithString("start", mcp.Description("New start time (RFC3339).")),
		mcp.WithString("end", mcp.Description("New end time (RFC3339).")),
	)
	s.AddTool(update, m.handleUpdateEvent)

	del := mcp.NewTool("delete_event",
		mcp.WithDescription("Delete an event from Eve's calendar by UID."),
		mcp.WithString("uid", mcp.Description("The UID of the event to delete.")),
	)
	s.AddTool(del, m.handleDeleteEvent)

	fb := mcp.NewTool("free_busy",
		mcp.WithDescription("List busy intervals on Eve's calendar within a time range."),
		mcp.WithString("from", mcp.Description("Start of range (RFC3339). Defaults to now.")),
		mcp.WithString("to", mcp.Description("End of range (RFC3339). Defaults to now+14 days.")),
	)
	s.AddTool(fb, m.handleFreeBusy)
}

func (m *MCP) handleGetCalendar(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	from, to, err := checkedCalRange(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	evs, err := m.mgr.Cal.ListRange(ctx, from, to)
	if err != nil {
		return mcp.NewToolResultError("calendar unavailable: " + err.Error()), nil
	}
	list := make([]CalEvent, 0)
	for _, ev := range evs {
		if ev.End.Before(from) || ev.Start.After(to) {
			continue
		}
		list = append(list, ev)
	}
	data, _ := json.Marshal(list)
	return mcp.NewToolResultText(string(data)), nil
}

func (m *MCP) handleCreateEvent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ev, err := calEventFromArgs(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := m.mgr.Cal.Put(ctx, ev); err != nil {
		return mcp.NewToolResultError("create failed: " + err.Error()), nil
	}
	return mcp.NewToolResultText("Event created: " + ev.UID), nil
}

func (m *MCP) handleUpdateEvent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uid, _ := req.GetArguments()["uid"].(string)
	if uid == "" {
		return mcp.NewToolResultError("uid is required"), nil
	}
	master, err := m.mgr.Cal.Find(ctx, uid)
	if err != nil {
		return mcp.NewToolResultError("calendar unavailable: " + err.Error()), nil
	}
	target := &master
	if v, ok := req.GetArguments()["summary"].(string); ok && v != "" {
		target.Summary = v
	}
	if v, ok := req.GetArguments()["start"].(string); ok {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return mcp.NewToolResultError("start must be RFC3339"), nil
		}
		target.Start = t
	}
	if v, ok := req.GetArguments()["end"].(string); ok {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return mcp.NewToolResultError("end must be RFC3339"), nil
		}
		target.End = t
	}
	if err := m.mgr.Cal.Put(ctx, *target); err != nil {
		return mcp.NewToolResultError("update failed: " + err.Error()), nil
	}
	return mcp.NewToolResultText("Event updated: " + uid), nil
}

func (m *MCP) handleDeleteEvent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uid, _ := req.GetArguments()["uid"].(string)
	if uid == "" {
		return mcp.NewToolResultError("uid is required"), nil
	}
	if err := m.mgr.Cal.Delete(ctx, uid); err != nil {
		return mcp.NewToolResultError("delete failed: " + err.Error()), nil
	}
	return mcp.NewToolResultText("Event deleted: " + uid), nil
}

func (m *MCP) handleFreeBusy(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	from, to, err := checkedCalRange(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	evs, err := m.mgr.Cal.ListRange(ctx, from, to)
	if err != nil {
		return mcp.NewToolResultError("calendar unavailable: " + err.Error()), nil
	}
	data, _ := json.Marshal(freeBusy(evs, from, to))
	return mcp.NewToolResultText(string(data)), nil
}

func calRange(req mcp.CallToolRequest) (from, to time.Time) {
	now := time.Now().UTC()
	from = now
	to = now.AddDate(0, 0, 14)
	if v, ok := req.GetArguments()["from"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v, ok := req.GetArguments()["to"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	return
}

func checkedCalRange(req mcp.CallToolRequest) (time.Time, time.Time, error) {
	from, to := calRange(req)
	for _, key := range []string{"from", "to"} {
		if value, ok := req.GetArguments()[key].(string); ok && value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return from, to, fmt.Errorf("%s must be RFC3339", key)
			}
		}
	}
	if !to.After(from) || to.Sub(from) > 366*24*time.Hour {
		return from, to, fmt.Errorf("range must be positive and at most 366 days")
	}
	return from, to, nil
}

func calEventFromArgs(req mcp.CallToolRequest) (CalEvent, error) {
	args := req.GetArguments()
	summary, _ := args["summary"].(string)
	startStr, _ := args["start"].(string)
	endStr, _ := args["end"].(string)
	if summary == "" || startStr == "" {
		return CalEvent{}, fmt.Errorf("summary and start are required")
	}
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		return CalEvent{}, fmt.Errorf("start must be RFC3339")
	}
	ev := CalEvent{UID: "eve-" + events.NewID() + "@local", Summary: summary, Start: start, End: start.Add(time.Hour)}
	if endStr != "" {
		t, err := time.Parse(time.RFC3339, endStr)
		if err != nil {
			return CalEvent{}, fmt.Errorf("end must be RFC3339")
		}
		ev.End = t
	}
	if r, ok := args["rrule"].(string); ok {
		if strings.ContainsAny(r, "\r\n") {
			return CalEvent{}, fmt.Errorf("invalid recurrence rule")
		}
		if _, err := rrule.StrToROption(r); r != "" && err != nil {
			return CalEvent{}, err
		}
		ev.RRule = r
	}
	if !ev.End.After(ev.Start) {
		return CalEvent{}, fmt.Errorf("end must be after start")
	}
	return ev, nil
}

// --- calendar poller -------------------------------------------------------

// CalPoller watches the calendar for events starting within the reminder lead
// time and hands them to the router for proactive delivery (notifications,
// reminders). Each event fires at most once per UID+start via a seen set.
type CalPoller struct {
	cal      *CalStore
	manager  *Manager
	interval time.Duration
}

func NewCalPoller(cal *CalStore, m *Manager, interval time.Duration) *CalPoller {
	pollInterval := 30 * time.Second
	if cal.cfg.ReminderLead > 0 {
		pollInterval = min(pollInterval, max(time.Second, cal.cfg.ReminderLead/2))
	}
	return &CalPoller{cal: cal, manager: m, interval: pollInterval}
}

// Run blocks until ctx is cancelled, checking for imminent events on each tick.
func (p *CalPoller) Run(ctx context.Context) {
	p.check(ctx)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.check(ctx)
		}
	}
}

func (p *CalPoller) check(ctx context.Context) {
	evs, err := p.cal.List(ctx)
	p.manager.RecordPollHealth("calendar", err)
	if err != nil {
		slog.Warn("calendar poll", "error", err)
		return
	}
	lead := p.cal.cfg.ReminderLead
	if lead == 0 {
		lead = 15 * time.Minute
	}
	for _, ev := range evs {
		if !ev.Start.After(time.Now()) || ev.Start.After(time.Now().Add(lead)) {
			continue
		}
		key := "calendar-" + ev.UID + "@" + ev.Start.UTC().Format("20060102T150405Z")
		msg := fmt.Sprintf("Reminder: %s at %s.", ev.Summary, ev.Start.Local().Format(time.RFC3339))
		if _, err := p.manager.EnqueueNotification(key, p.manager.store.PrimaryConversationID(), msg, PurposeReminder, ""); err != nil {
			p.manager.RecordPollHealth("calendar", err)
			slog.Warn("calendar reminder enqueue", "uid", ev.UID, "error", err)
		}
	}
}
