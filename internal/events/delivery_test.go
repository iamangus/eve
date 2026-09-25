package events

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/store"
)

func TestDeliveryWaitsForAgentInputReceipt(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
			w.Write([]byte(`{"run_id":"run-1"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/inputs"):
			w.Write([]byte(`{"run_id":"run-1"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/inputs/event-1"):
			status := "accepted"
			if polls.Add(1) > 1 {
				status = "processed"
			}
			json.NewEncoder(w).Encode(map[string]string{"status": status})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := agentfoundry.NewClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := NewQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(queue, st, client, "backend-agent", "frontend-agent", "", nil, nil)
	if err := h.deliver(context.Background(), &Event{Source: "opendev", Category: "backend", ID: "event-1", Payload: json.RawMessage(`{"job":"ready"}`)}); err != nil {
		t.Fatal(err)
	}
	if polls.Load() != 2 {
		t.Fatalf("delivery completed before processing: %d receipt polls", polls.Load())
	}
}

func TestConfirmedAgentFailureRetriesWithFreshInputID(t *testing.T) {
	var accepted []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
			w.Write([]byte(`{"run_id":"run-1"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/inputs"):
			var input struct {
				InputID string `json:"input_id"`
			}
			json.NewDecoder(r.Body).Decode(&input)
			accepted = append(accepted, input.InputID)
			w.Write([]byte(`{"run_id":"run-1"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/inputs/event-1"):
			w.Write([]byte(`{"status":"failed"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/inputs/event-1-retry-1"):
			w.Write([]byte(`{"status":"processed"}`))
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := agentfoundry.NewClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := NewQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue("opendev", "backend", "event-1", json.RawMessage(`{"job":"ready"}`)); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(queue, st, client, "backend-agent", "frontend-agent", "", nil, nil)
	if err := h.processOne(context.Background()); !errors.Is(err, errAgentInputFailed) {
		t.Fatalf("expected acknowledged agent failure, got %v", err)
	}
	if queue.events[eventKey("opendev", "event-1")].InputID != "event-1-retry-1" {
		t.Fatal("failed input ID was not rotated")
	}
	time.Sleep(1100 * time.Millisecond)
	if err := h.processOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(accepted) != 2 || accepted[0] != "event-1" || accepted[1] != "event-1-retry-1" || queue.events[eventKey("opendev", "event-1")].State != StateDone {
		t.Fatalf("event retry: inputs=%v state=%+v", accepted, queue.events[eventKey("opendev", "event-1")])
	}
}

func TestDeletedConversationEventBecomesFailedRecord(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := NewQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue("opendev", "frontend", "event-1", json.RawMessage(`{"conversation_id":"deleted"}`)); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(queue, st, nil, "backend", "frontend", "", nil, nil)
	if err := h.processOne(context.Background()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected deleted conversation, got %v", err)
	}
	failed := queue.events[eventKey("opendev", "event-1")]
	if failed.State != StateFailed || failed.LastError == "" {
		t.Fatalf("event was retried forever: %+v", failed)
	}
}

func TestFrontendEventDoesNotStartRunDuringConversationDeletion(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	var inputs, cancels atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
			if _, err := st.BeginConversationDelete(conv.ID); err != nil {
				t.Error(err)
			}
			w.Write([]byte(`{"run_id":"run-1"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel"):
			cancels.Add(1)
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/inputs"):
			inputs.Add(1)
			w.Write([]byte(`{"run_id":"run-1"}`))
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := agentfoundry.NewClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	queue, err := NewQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(queue, st, client, "backend", "frontend", "", nil, nil)
	payload, _ := json.Marshal(map[string]string{"conversation_id": conv.ID})
	if err := h.deliver(context.Background(), &Event{Source: "opendev", Category: "frontend", ID: "event-1", Payload: payload}); !errors.Is(err, store.ErrDeleteInProgress) {
		t.Fatalf("expected deletion gate, got %v", err)
	}
	st.EndConversationDelete(conv.ID)
	if inputs.Load() != 0 || cancels.Load() != 1 || st.PersistentRun("frontend-events:"+conv.ID) != "" {
		t.Fatalf("orphaned event run: inputs=%d cancels=%d", inputs.Load(), cancels.Load())
	}
}

func TestFrontendEventRetriesWhileConversationDeletionIsUnresolved(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	queue, err := NewQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"conversation_id": conv.ID})
	if _, err := queue.Enqueue("opendev", "frontend", "event-1", payload); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BeginConversationDelete(conv.ID); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(queue, st, nil, "backend", "frontend", "", nil, nil)
	if err := h.processOne(context.Background()); !errors.Is(err, store.ErrDeleteInProgress) {
		t.Fatalf("expected transient deletion, got %v", err)
	}
	st.EndConversationDelete(conv.ID)
	if event, ok := queue.Status("opendev", "event-1"); !ok || event.State != StatePending {
		t.Fatalf("event was permanently failed during an unsuccessful deletion: %+v", event)
	}
}

func TestOpenDevEventStatusIsAuthenticatedAndDurable(t *testing.T) {
	dir := t.TempDir()
	queue, err := NewQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue("opendev", "backend", "event-1", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(queue, nil, nil, "", "", "secret", nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	request := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/inbound/opendev/events/event-1", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, req)
		return resp
	}
	if got := request("").Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthorized status lookup: %d", got)
	}
	if got := request("secret"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"state":"pending"`) {
		t.Fatalf("pending event status: %d %s", got.Code, got.Body.String())
	}
	leased, err := queue.Lease(time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Complete(leased); err != nil {
		t.Fatal(err)
	}
	queue, err = NewQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	h.queue = queue
	if got := request("secret"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"state":"done"`) {
		t.Fatalf("restored event status: %d %s", got.Code, got.Body.String())
	}
}
