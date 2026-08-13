package io

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
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
	return base
}

// CalEvent is a parsed iCalendar event as Eve understands it.
type CalEvent struct {
	UID     string
	Summary string
	Start   time.Time
	End     time.Time
	RRule   string
}

// CalStore is a thin CalDAV client speaking only what we need: fetch the
// whole collection, put an event, delete an event. All bodies are .ics
// text/calendar payloads; auth is HTTP Basic.
type CalStore struct {
	cfg CalDAVConfig
	hc  *http.Client
}

func NewCalStore(cfg CalDAVConfig) *CalStore {
	return &CalStore{cfg: cfg, hc: &http.Client{Timeout: 30 * time.Second}}
}

// List fetches the calendar collection and parses all VEVENTs.
func (c *CalStore) List(ctx context.Context) ([]CalEvent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.calendarURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/calendar")
	c.auth(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return nil, fmt.Errorf("caldav get %s: %s", resp.Status, body)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseICalendar(string(data)), nil
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
		ev.UID = fmt.Sprintf("eve-%d@local", time.Now().UnixNano())
	}
	resource += ev.UID + ".ics"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, resource, strings.NewReader(ev.ICS()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
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
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.cfg.calendarURL()+uid+".ics", nil)
	if err != nil {
		return err
	}
	c.auth(req)
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
	uid := ev.UID
	if uid == "" {
		uid = fmt.Sprintf("eve-%d@local", time.Now().UnixNano())
	}
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//eve//calendar//EN\r\n")
	b.WriteString("BEGIN:VEVENT\r\n")
	fmt.Fprintf(&b, "UID:%s\r\n", uid)
	if ev.Summary != "" {
		b.WriteString("SUMMARY:")
		b.WriteString(strings.ReplaceAll(ev.Summary, "\n", "\\n"))
		b.WriteString("\r\n")
	}
	b.WriteString("DTSTART:")
	b.WriteString(ev.Start.UTC().Format("20060102T150405Z"))
	b.WriteString("\r\n")
	if !ev.End.IsZero() {
		b.WriteString("DTEND:")
		b.WriteString(ev.End.UTC().Format("20060102T150405Z"))
		b.WriteString("\r\n")
	}
	if ev.RRule != "" {
		b.WriteString("RRULE:")
		b.WriteString(ev.RRule)
		b.WriteString("\r\n")
	}
	b.WriteString("END:VEVENT\r\n")
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

// --- minimal iCalendar parsing ---------------------------------------------

var (
	lineSplitRe = regexp.MustCompile(`\r?\n`)
	propRe      = regexp.MustCompile(`^([A-Z]+)(;[^:]*)?:(.*)$`)
)

// parseICalendar extracts VEVENTs from an .ics payload. Recurring events are
// expanded only for the quiet-hours pattern (FREQ=DAILY with DURATION via the
// event span): for the next 60 days each occurrence becomes a concrete event.
func parseICalendar(data string) []CalEvent {
	var out []CalEvent
	lines := lineSplitRe.Split(data, -1)
	var cur *CalEvent
	inVEVENT := false
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(line, "BEGIN:VEVENT") {
			inVEVENT = true
			cur = &CalEvent{}
			continue
		}
		if strings.HasPrefix(line, "END:VEVENT") {
			if cur != nil && !cur.Start.IsZero() {
				out = append(out, *cur)
			}
			inVEVENT = false
			cur = nil
			continue
		}
		if !inVEVENT || cur == nil {
			continue
		}
		m := propRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, params, val := m[1], m[2], strings.TrimSpace(m[3])
		switch key {
		case "UID":
			cur.UID = val
		case "SUMMARY":
			cur.Summary = val
		case "DTSTART":
			cur.Start = parseICalTime(val, params)
		case "DTEND":
			cur.End = parseICalTime(val, params)
		case "RRULE":
			cur.RRule = val
		}
	}
	return expandRecurring(out)
}

// parseICalTime handles the two common DTSTART forms: UTC (20060102T150405Z)
// and date-only. Zone-less values are treated as UTC.
func parseICalTime(val, params string) time.Time {
	val = strings.TrimSuffix(val, "Z")
	if t, err := time.Parse("20060102T150405", val); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse("20060102", val); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// expandRecurring materializes DAILY recurring events (quiet hours) into
// concrete occurrences over the next 60 days.
func expandRecurring(evs []CalEvent) []CalEvent {
	var out []CalEvent
	for _, ev := range evs {
		if ev.RRule == "" || !strings.HasPrefix(ev.RRule, "FREQ=DAILY") {
			out = append(out, ev)
			continue
		}
		dur := ev.End.Sub(ev.Start)
		if dur <= 0 {
			dur = time.Hour
		}
		for i := 0; i < 60; i++ {
			o := ev
			o.RRule = ""
			o.Start = ev.Start.AddDate(0, 0, i)
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
	var out []map[string]string
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
	evs, err := m.mgr.Cal.List(ctx)
	if err != nil {
		return mcp.NewToolResultError("calendar unavailable: " + err.Error()), nil
	}
	from, to := calRange(req)
	var list []CalEvent
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
	evs, err := m.mgr.Cal.List(ctx)
	if err != nil {
		return mcp.NewToolResultError("calendar unavailable: " + err.Error()), nil
	}
	var target *CalEvent
	for i := range evs {
		if evs[i].UID == uid {
			target = &evs[i]
			break
		}
	}
	if target == nil {
		return mcp.NewToolResultError("no event with uid " + uid), nil
	}
	if v, ok := req.GetArguments()["summary"].(string); ok && v != "" {
		target.Summary = v
	}
	if v, ok := req.GetArguments()["start"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			target.Start = t
		}
	}
	if v, ok := req.GetArguments()["end"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			target.End = t
		}
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
	evs, err := m.mgr.Cal.List(ctx)
	if err != nil {
		return mcp.NewToolResultError("calendar unavailable: " + err.Error()), nil
	}
	from, to := calRange(req)
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
	ev := CalEvent{Summary: summary, Start: start}
	if endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			ev.End = t
		}
	}
	if r, ok := args["rrule"].(string); ok {
		ev.RRule = r
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
	seen     map[string]bool
}

func NewCalPoller(cal *CalStore, m *Manager, interval time.Duration) *CalPoller {
	return &CalPoller{cal: cal, manager: m, interval: interval, seen: map[string]bool{}}
}

// Run blocks until ctx is cancelled, checking for imminent events on each tick.
func (p *CalPoller) Run(ctx context.Context) {
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
	if err != nil {
		slog.Warn("calendar poll", "error", err)
		return
	}
	lead := p.cal.cfg.ReminderLead
	if lead == 0 {
		lead = 15 * time.Minute
	}
	ev, ok := upcomingReminder(evs, lead)
	if !ok {
		return
	}
	key := ev.UID + "@" + ev.Start.Format("20060102T150405Z")
	if p.seen[key] {
		return
	}
	p.seen[key] = true
	msg := fmt.Sprintf("Reminder: %s at %s.", ev.Summary, ev.Start.Local().Format(time.RFC3339))
	if err := p.manager.Router.Notify(ctx, p.manager.store.PrimaryConversationID(), msg, PurposeReminder, ""); err != nil {
		slog.Warn("calendar reminder notify", "uid", ev.UID, "error", err)
	}
}
