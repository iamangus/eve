package io

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/iamangus/eve/internal/store"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestMatrixPrivacyRequiresExactlyBotAndOwner(t *testing.T) {
	members := []string{"@eve:test", "@owner:test"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		joined := make(map[string]any)
		for _, user := range members {
			joined[user] = map[string]string{}
		}
		json.NewEncoder(w).Encode(map[string]any{"joined": joined})
	}))
	defer server.Close()
	resolver, _ := LoadResolver(t.TempDir())
	if err := resolver.Upsert(Identity{Name: "owner", Owner: true, Channels: []IdentityChannel{{Type: ChannelMatrix, Address: "@owner:test"}}}); err != nil {
		t.Fatal(err)
	}
	cfg := MatrixConfig{Homeserver: server.URL, UserID: "@eve:test", AccessToken: "token"}
	if err := cfg.privateRoom(context.Background(), "!room:test", resolver); err != nil {
		t.Fatal(err)
	}
	e2ee := &MatrixE2EE{mach: &crypto.OlmMachine{AllowKeyShare: func(context.Context, *id.Device, event.RequestedKeyInfo) *crypto.KeyShareRejection { return nil }}}
	e2ee.setPrivacyPolicy(cfg, resolver)
	info := event.RequestedKeyInfo{RoomID: "!room:test"}
	if rejection := e2ee.mach.AllowKeyShare(context.Background(), &id.Device{UserID: "@other:test"}, info); rejection == nil {
		t.Fatal("private room key shared with a third party")
	}
	if rejection := e2ee.mach.AllowKeyShare(context.Background(), &id.Device{UserID: "@owner:test"}, info); rejection != nil {
		t.Fatal("owner key request rejected in a private room")
	}
	members = append(members, "@other:test")
	if rejection := e2ee.mach.AllowKeyShare(context.Background(), &id.Device{UserID: "@owner:test"}, info); rejection == nil {
		t.Fatal("room keys shared after room became shared")
	}
	if err := cfg.privateRoom(context.Background(), "!room:test", resolver); err == nil {
		t.Fatal("shared room considered private")
	}
	members = []string{"@eve:test", "@stranger:test"}
	if err := cfg.privateRoom(context.Background(), "!room:test", resolver); err == nil {
		t.Fatal("unknown member considered owner")
	}
}

func TestCryptoReplayIndicesSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crypto.json")
	st, err := openJSONCryptoStore(path, []byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	session, err := crypto.NewOutboundGroupSession("!room:test", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.MaxAge = 2 * time.Hour
	if err := st.ms.AddOutboundGroupSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.ms.ValidateMessageIndex(context.Background(), id.SessionID("session:with:colon"), id.EventID("event1"), 42, 123); err != nil || !ok {
		t.Fatalf("index: %t %v", ok, err)
	}
	reloaded, err := openJSONCryptoStore(path, []byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := reloaded.ms.GetOutboundGroupSession(context.Background(), "!room:test")
	if restored == nil || restored.MaxAge != session.MaxAge {
		t.Fatal("session rotation policy lost after restart")
	}
	if ok, _ := reloaded.ms.ValidateMessageIndex(context.Background(), id.SessionID("session:with:colon"), id.EventID("event2"), 42, 124); ok {
		t.Fatal("replay protection lost after restart")
	}
	if err := st.SaveNextBatch(context.Background(), "@eve:test", "unaccepted-batch"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveFilterID(context.Background(), "@eve:test", "filter"); err != nil {
		t.Fatal(err)
	}
	reloaded, err = openJSONCryptoStore(path, []byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	if token, _ := reloaded.LoadNextBatch(context.Background(), "@eve:test"); token != "" {
		t.Fatal("sync token committed before durable acceptance")
	}
}

func TestNotificationOutboxIsDurableAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.New(dir)
	conv, _ := st.CreateConversation("chat")
	m, err := NewManager(st, nil, Config{DataDir: dir, ProactiveEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.EnqueueNotification("delivery1", conv.ID, "hello", PurposeNotification, "web"); err != nil {
		t.Fatal(err)
	}
	m, err = NewManager(st, nil, Config{DataDir: dir, ProactiveEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.deliverNotification(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.EnqueueNotification("delivery1", conv.ID, "hello", PurposeNotification, "web"); err != nil {
		t.Fatal(err)
	}
	if err := m.deliverNotification(context.Background()); err != nil {
		t.Fatal(err)
	}
	full, _ := st.GetConversation(conv.ID)
	if len(full.Messages) != 1 {
		t.Fatalf("notification duplicated: %+v", full.Messages)
	}
}

func TestCalendarRecurrenceHonorsTimezoneAndExceptions(t *testing.T) {
	text := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:series\r\nSUMMARY:Test\\, event\r\nDTSTART;TZID=America/New_York:20260301T090000\r\nDTEND;TZID=America/New_York:20260301T100000\r\nRRULE:FREQ=WEEKLY;COUNT=3\r\nEXDATE;TZID=America/New_York:20260308T090000\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	from, _ := time.Parse(time.RFC3339, "2026-03-01T00:00:00Z")
	to := from.AddDate(0, 1, 0)
	cal, err := ical.NewDecoder(strings.NewReader(text)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	events, err := calendarEvents(cal, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected two occurrences, got %+v", events)
	}
	if events[0].Start.UTC().Hour() != 14 || events[1].Start.UTC().Hour() != 13 {
		t.Fatalf("DST ignored: %+v", events)
	}
	if events[0].Summary != "Test, event" {
		t.Fatal("calendar text was not decoded")
	}
	encoded, err := encodeCalendar(events[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, "RRULE:") || !strings.Contains(encoded, "EXDATE") {
		t.Fatal("series information lost")
	}
}
