package io

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCalDAVUsesResourceHrefAndConditionalWrites(t *testing.T) {
	writes := 0
	calendar := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:different-from-filename\r\nDTSTAMP:20261019T000000Z\r\nDTSTART:20261020T090000Z\r\nDTEND:20261020T100000Z\r\nSUMMARY:Original\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "REPORT" {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(207)
			io.WriteString(w, `<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response><d:href>/cal/random.ics</d:href><d:propstat><d:prop><d:getetag>"version1"</d:getetag><c:calendar-data><![CDATA[`+calendar+`]]></c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`)
			return
		}
		if r.Method != http.MethodPut || r.URL.Path != "/cal/random.ics" || r.Header.Get("If-Match") != `"version1"` {
			t.Errorf("unsafe write: %s %s %v", r.Method, r.URL.Path, r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "SUMMARY:Changed") {
			t.Error("event change missing")
		}
		writes++
		w.WriteHeader(204)
	}))
	defer server.Close()
	cal := NewCalStore(CalDAVConfig{URL: server.URL, CalendarPath: "cal"})
	event, err := cal.Find(context.Background(), "different-from-filename")
	if err != nil {
		t.Fatal(err)
	}
	event.Summary = "Changed"
	if err := cal.Put(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatal("write not sent")
	}
}
