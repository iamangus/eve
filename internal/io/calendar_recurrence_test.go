package io

import (
	"github.com/emersion/go-ical"
	"strings"
	"testing"
	"time"
)

func TestCalendarHandlesCommaSeparatedRecurrenceDatesWithoutRule(t *testing.T) {
	text := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:dates\r\nDTSTAMP:20261019T000000Z\r\nDTSTART:20261020T090000Z\r\nDTEND:20261020T100000Z\r\nRDATE:20261021T090000Z,20261022T090000Z\r\nEXDATE:20261020T090000Z,20261021T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cal, err := ical.NewDecoder(strings.NewReader(text)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	list, err := calendarEvents(cal, from, from.AddDate(0, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Start.Day() != 22 {
		t.Fatalf("recurrence date lists ignored: %+v", list)
	}
}

func TestCalendarSummaryEditPreservesAllDayAndSeriesTimezone(t *testing.T) {
	for _, date := range []string{"DTSTART;VALUE=DATE:20261020\r\nDTEND;VALUE=DATE:20261021", "DTSTART;TZID=America/New_York:20261020T090000\r\nDTEND;TZID=America/New_York:20261020T100000"} {
		text := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:dates\r\nDTSTAMP:20261019T000000Z\r\n" + date + "\r\nRRULE:FREQ=DAILY;COUNT=3\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
		cal, err := ical.NewDecoder(strings.NewReader(text)).Decode()
		if err != nil {
			t.Fatal(err)
		}
		ev, err := componentEvent(cal.Children[0], cal)
		if err != nil {
			t.Fatal(err)
		}
		ev.Summary = "Changed"
		encoded, err := encodeCalendar(ev)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(date, "\r\n") {
			if !strings.Contains(encoded, line) {
				t.Fatalf("summary edit changed date semantics: %s", encoded)
			}
		}
	}
}
