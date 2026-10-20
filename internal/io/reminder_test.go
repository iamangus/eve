package io

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iamangus/eve/internal/store"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestAllDueRemindersRetryFailedAcceptanceAndSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.New(dir)
	conv, _ := st.CreateConversation("chat")
	manager, _ := NewManager(st, nil, Config{DataDir: dir, ProactiveEnabled: true})
	start := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Second)
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n"
	for _, uid := range []string{"one", "two"} {
		ics += "BEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:" + start.Format("20060102T150405Z") + "\r\nDTSTART:" + start.Format("20060102T150405Z") + "\r\nDTEND:" + start.Add(time.Hour).Format("20060102T150405Z") + "\r\nSUMMARY:" + uid + "\r\nEND:VEVENT\r\n"
	}
	ics += "END:VCALENDAR\r\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(207)
		io.WriteString(w, `<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response><d:href>/cal/events.ics</d:href><d:propstat><d:prop><d:getetag>"v1"</d:getetag><c:calendar-data><![CDATA[`+ics+`]]></c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`)
	}))
	defer server.Close()
	cal := NewCalStore(CalDAVConfig{URL: server.URL, CalendarPath: "cal", ReminderLead: 15 * time.Minute})
	poller := NewCalPoller(cal, manager, time.Hour)
	tmp := filepath.Join(dir, "outbound", "internal_events.json.tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	poller.check(context.Background())
	if len(manager.Outbox.Snapshot()) != 0 {
		t.Fatal("failed acceptance recorded as sent")
	}
	if err := os.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	poller.check(context.Background())
	if len(manager.Outbox.Snapshot()) != 2 {
		t.Fatal("not all due reminders queued")
	}
	for i := 0; i < 2; i++ {
		if err := manager.deliverNotification(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	manager, _ = NewManager(st, nil, Config{DataDir: dir, ProactiveEnabled: true})
	poller = NewCalPoller(cal, manager, time.Hour)
	poller.check(context.Background())
	if err := manager.deliverNotification(context.Background()); err != nil {
		t.Fatal(err)
	}
	full, _ := st.GetConversation(conv.ID)
	if len(full.Messages) != 2 {
		t.Fatal("restart duplicated reminder")
	}
}

func TestUnknownOrDowngradedEncryptionStateNeverSendsPlaintext(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			sends := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					sends++
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				io.WriteString(w, `{"errcode":"M_NOT_FOUND","error":"unavailable"}`)
			}))
			defer server.Close()
			client, _ := mautrix.NewClient(server.URL, "@eve:test", "token")
			client.StateStore = mautrix.NewMemoryStateStore()
			client.StateStore.SetEncryptionEvent(context.Background(), id.RoomID("!room:test"), &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1})
			adapter := &matrixAdapter{e2ee: &MatrixE2EE{client: client}}
			if err := adapter.sendText(context.Background(), "!room:test", "private reply", "turn"); err == nil {
				t.Fatal("unknown encryption state permitted send")
			}
			if sends != 0 {
				t.Fatal("private reply sent without verified encryption state")
			}
		})
	}
}
