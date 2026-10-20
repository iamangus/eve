package io

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/teambition/rrule-go"
)

type calendarHTTP struct{ store *CalStore }
type limitedCalendarBody struct {
	io.Reader
	io.Closer
}

func (c calendarHTTP) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.store.hc.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body = limitedCalendarBody{io.LimitReader(resp.Body, 16<<20), resp.Body}
	return resp, nil
}

func (c *CalStore) calendarClient(ctx context.Context) (*caldav.Client, string, error) {
	client, err := caldav.NewClient(webdav.HTTPClientWithBasicAuth(calendarHTTP{c}, c.cfg.Username, c.cfg.Password), c.cfg.URL)
	if err != nil {
		return nil, "", err
	}
	collection := c.cfg.calendarURL()
	if c.cfg.CalendarPath == "" {
		principal, err := client.FindCurrentUserPrincipal(ctx)
		if err != nil {
			return nil, "", err
		}
		home, err := client.FindCalendarHomeSet(ctx, principal)
		if err != nil {
			return nil, "", err
		}
		calendars, err := client.FindCalendars(ctx, home)
		if err != nil {
			return nil, "", err
		}
		if len(calendars) != 1 {
			return nil, "", fmt.Errorf("configure CALDAV_CALENDAR_PATH when discovery returns %d calendars", len(calendars))
		}
		collection = calendars[0].Path
	}
	return client, collection, nil
}

func (c *CalStore) resources(ctx context.Context) ([]caldav.CalendarObject, error) {
	client, collection, err := c.calendarClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.QueryCalendar(ctx, collection, &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{Name: ical.CompCalendar, AllProps: true, AllComps: true},
		CompFilter:  caldav.CompFilter{Name: ical.CompCalendar, Comps: []caldav.CompFilter{{Name: ical.CompEvent}}},
	})
}

func (c *CalStore) resourceURL(path string) (string, error) {
	base, err := url.Parse(c.cfg.URL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	target := base.ResolveReference(ref)
	if target.Scheme != base.Scheme || target.Host != base.Host || target.User != nil {
		return "", fmt.Errorf("cross-origin calendar resource rejected")
	}
	return target.String(), nil
}

func (c *CalStore) ListRange(ctx context.Context, from, to time.Time) ([]CalEvent, error) {
	if !to.After(from) || to.Sub(from) > 366*24*time.Hour {
		return nil, fmt.Errorf("calendar range must be positive and no longer than 366 days")
	}
	objects, err := c.resources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CalEvent, 0)
	for _, object := range objects {
		if object.Data == nil {
			return nil, fmt.Errorf("calendar REPORT omitted event data")
		}
		events, err := calendarEvents(object.Data, from, to)
		if err != nil {
			return nil, err
		}
		resource, err := c.resourceURL(object.Path)
		if err != nil {
			return nil, err
		}
		for _, ev := range events {
			ev.Resource = resource
			ev.ETag = object.ETag
			out = append(out, ev)
		}
		if len(out) > 10000 {
			return nil, fmt.Errorf("calendar result exceeds 10000 occurrences")
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

func (c *CalStore) Find(ctx context.Context, uid string) (CalEvent, error) {
	objects, err := c.resources(ctx)
	if err != nil {
		return CalEvent{}, err
	}
	for _, object := range objects {
		if object.Data == nil {
			continue
		}
		for _, component := range object.Data.Children {
			if component.Name != ical.CompEvent || component.Props.Get("RECURRENCE-ID") != nil {
				continue
			}
			ev, err := componentEvent(component, object.Data)
			if err != nil {
				return CalEvent{}, err
			}
			if ev.UID == uid {
				ev.Resource, err = c.resourceURL(object.Path)
				ev.ETag = object.ETag
				if err == nil && ev.ETag == "" {
					err = fmt.Errorf("calendar resource has no ETag")
				}
				return ev, err
			}
		}
	}
	return CalEvent{}, fmt.Errorf("no calendar event with UID %q", uid)
}

func componentEvent(comp *ical.Component, raw *ical.Calendar) (CalEvent, error) {
	ev := CalEvent{raw: raw}
	var err error
	ev.UID, err = comp.Props.Text("UID")
	if err != nil || ev.UID == "" {
		return ev, fmt.Errorf("calendar event has no UID")
	}
	ev.Summary, _ = comp.Props.Text("SUMMARY")
	event := ical.Event{Component: comp}
	ev.Start, err = event.DateTimeStart(time.UTC)
	if err != nil {
		return ev, err
	}
	ev.End, err = event.DateTimeEnd(time.UTC)
	if err != nil {
		return ev, err
	}
	if ev.End.IsZero() {
		ev.End = ev.Start
	}
	if prop := comp.Props.Get("RRULE"); prop != nil {
		ev.RRule = prop.Value
	}
	if prop := comp.Props.Get("RECURRENCE-ID"); prop != nil {
		ev.RecurrenceID = prop.Value
	}
	return ev, nil
}

func calendarEvents(cal *ical.Calendar, from, to time.Time) ([]CalEvent, error) {
	var out []CalEvent
	overrides := make(map[string]bool)
	for _, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}
		if prop := comp.Props.Get("RECURRENCE-ID"); prop != nil {
			t, err := prop.DateTime(time.UTC)
			if err != nil {
				return nil, err
			}
			uid, _ := comp.Props.Text("UID")
			overrides[uid+"@"+t.UTC().Format(time.RFC3339)] = true
		}
	}
	for _, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}
		if status, _ := comp.Props.Text("STATUS"); status == "CANCELLED" {
			continue
		}
		ev, err := componentEvent(comp, cal)
		if err != nil {
			return nil, err
		}
		set, err := recurrenceSet(comp)
		if err != nil {
			return nil, err
		}
		if set == nil || ev.RecurrenceID != "" {
			if !ev.End.Before(from) && ev.Start.Before(to) {
				out = append(out, ev)
			}
			continue
		}
		duration := ev.End.Sub(ev.Start)
		next := set.Iterator()
		for count := 0; ; count++ {
			if count >= 100000 {
				return nil, fmt.Errorf("recurrence expansion exceeds safety limit")
			}
			start, ok := next()
			if !ok || !start.Before(to) {
				break
			}
			if start.Add(duration).Before(from) || overrides[ev.UID+"@"+start.UTC().Format(time.RFC3339)] {
				continue
			}
			occurrence := ev
			occurrence.Start, occurrence.End = start, start.Add(duration)
			out = append(out, occurrence)
			if len(out) > 10000 {
				return nil, fmt.Errorf("recurrence exceeds 10000 occurrences")
			}
		}
	}
	return out, nil
}

func recurrenceSet(comp *ical.Component) (*rrule.Set, error) {
	if comp.Props.Get("RRULE") == nil && comp.Props.Get("RDATE") == nil {
		return nil, nil
	}
	start, err := comp.Props.DateTime("DTSTART", time.UTC)
	if err != nil {
		return nil, err
	}
	set := &rrule.Set{}
	set.DTStart(start)
	set.RDate(start)
	if rule := comp.Props.Get("RRULE"); rule != nil {
		option, err := rrule.StrToROptionInLocation(rule.Value, start.Location())
		if err != nil {
			return nil, err
		}
		option.Dtstart = start
		parsed, err := rrule.NewRRule(*option)
		if err != nil {
			return nil, err
		}
		set.RRule(parsed)
	}
	for _, key := range []string{"RDATE", "EXDATE"} {
		for _, prop := range comp.Props.Values(key) {
			for _, value := range strings.Split(prop.Value, ",") {
				copy := prop
				copy.Value = value
				date, err := copy.DateTime(start.Location())
				if err != nil {
					return nil, err
				}
				if key == "RDATE" {
					set.RDate(date)
				} else {
					set.ExDate(date)
				}
			}
		}
	}
	return set, nil
}

func encodeCalendar(ev CalEvent) (string, error) {
	cal := ical.NewCalendar()
	cal.Props.SetText("VERSION", "2.0")
	cal.Props.SetText("PRODID", "-//eve//calendar//EN")
	if ev.raw != nil {
		data, err := json.Marshal(ev.raw)
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(data, &cal); err != nil {
			return "", err
		}
	}
	if cal.Props.Get("VERSION") == nil {
		cal.Props.SetText("VERSION", "2.0")
	}
	if cal.Props.Get("PRODID") == nil {
		cal.Props.SetText("PRODID", "-//eve//calendar//EN")
	}
	var target *ical.Component
	for _, comp := range cal.Children {
		uid, _ := comp.Props.Text("UID")
		if comp.Name == ical.CompEvent && uid == ev.UID && comp.Props.Get("RECURRENCE-ID") == nil {
			target = comp
			break
		}
	}
	if target == nil {
		target = ical.NewComponent(ical.CompEvent)
		cal.Children = append(cal.Children, target)
	}
	target.Props.SetText("UID", ev.UID)
	target.Props.SetText("SUMMARY", ev.Summary)
	target.Props.SetDateTime("DTSTAMP", time.Now().UTC())
	setCalendarTime(target, "DTSTART", ev.Start)
	originalEnd, err := (&ical.Event{Component: target}).DateTimeEnd(time.UTC)
	if err != nil || !originalEnd.Equal(ev.End) || target.Props.Get("DURATION") == nil {
		setCalendarTime(target, "DTEND", ev.End)
		target.Props.Del("DURATION")
	}
	if ev.RRule != "" {
		prop := ical.NewProp("RRULE")
		prop.Value = ev.RRule
		target.Props.Set(prop)
	} else {
		target.Props.Del("RRULE")
	}
	var data strings.Builder
	if err := ical.NewEncoder(&data).Encode(cal); err != nil {
		return "", err
	}
	return data.String(), nil
}

func setCalendarTime(target *ical.Component, key string, value time.Time) {
	if prop := target.Props.Get(key); prop != nil {
		if original, err := prop.DateTime(time.UTC); err == nil && original.Equal(value) {
			return
		}
		if tzid := prop.Params.Get("TZID"); tzid != "" {
			if location, err := time.LoadLocation(tzid); err == nil {
				target.Props.SetDateTime(key, value.In(location))
				return
			}
		}
	}
	if value.Location().String() == "" || strings.HasPrefix(value.Location().String(), "+") || strings.HasPrefix(value.Location().String(), "-") {
		value = value.UTC()
	}
	target.Props.SetDateTime(key, value)
}
