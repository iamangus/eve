package trigger

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/store"
)

func TestTriggerAcceptanceAndCompletionSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.NewEmailStore(dir)
	account, _ := st.AddAccount(store.Account{Address: "inbox@example.com", UIDValidity: 2})
	st.AddTrigger(store.Trigger{AccountID: account.ID, Name: "all", Enabled: true, Prompt: "summarize"})
	dispatches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			dispatches++
			var input struct {
				TaskID string `json:"task_id"`
			}
			json.NewDecoder(r.Body).Decode(&input)
			if input.TaskID == "" {
				t.Error("trigger dispatched without idempotency key")
			}
			json.NewEncoder(w).Encode(map[string]string{"run_id": "run1"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "completed", "response": "done"})
	}))
	defer server.Close()
	client, _ := agentfoundry.NewClient(server.URL, "key")
	engine := NewEngine(st, client, "eve", time.Minute)
	msg := store.EmailMessage{UID: 3, MessageID: "message1", Body: "hello"}
	if err := engine.HandleEmail(context.Background(), account, msg); err != nil {
		t.Fatal(err)
	}
	if dispatches != 0 || len(st.Runs()) != 1 || st.Runs()[0].Status != "pending" {
		t.Fatal("acceptance did not persist before dispatch")
	}
	st, _ = store.NewEmailStore(dir)
	engine = NewEngine(st, client, "eve", time.Minute)
	engine.poll(context.Background())
	st, _ = store.NewEmailStore(dir)
	engine = NewEngine(st, client, "eve", time.Minute)
	engine.poll(context.Background())
	if err := engine.HandleEmail(context.Background(), account, msg); err != nil {
		t.Fatal(err)
	}
	if dispatches != 1 || len(st.Runs()) != 1 || st.Runs()[0].Status != "completed" {
		t.Fatalf("recovery or deduplication failed: dispatches=%d runs=%+v", dispatches, st.Runs())
	}
}

func TestTriggerDoesNotAcknowledgeFailedDurableAcceptance(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.NewEmailStore(dir)
	account, _ := st.AddAccount(store.Account{Address: "inbox@example.com"})
	st.AddTrigger(store.Trigger{AccountID: account.ID, Name: "all", Enabled: true, Prompt: "summarize"})
	if err := os.Mkdir(filepath.Join(dir, "email_state.json.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(st, nil, "eve", time.Minute)
	if err := engine.HandleEmail(context.Background(), account, store.EmailMessage{UID: 1, MessageID: "message1"}); err == nil {
		t.Fatal("acknowledged failed persistence")
	}
	if st.IsProcessed(account.ID, "message1") || len(st.Runs()) != 0 {
		t.Fatal("failed acceptance changed durable state")
	}
}
