package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/io"
	"github.com/iamangus/eve/internal/store"
)

func TestQueuedConversationRepliesCorrelateWithEachTurn(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var lastInput, response string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/runs"):
			json.NewEncoder(w).Encode(map[string]string{"run_id": "run-1"})
		case strings.HasSuffix(r.URL.Path, "/inputs"):
			var input struct {
				InputID string `json:"input_id"`
				Message string `json:"message"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			lastInput, response = input.InputID, "answer to "+input.Message
			json.NewEncoder(w).Encode(map[string]string{"run_id": "run-1"})
		case r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"status": "waiting", "persistent": true, "last_input_id": lastInput, "response": response})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := agentfoundry.NewClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := io.NewManager(st, client, io.Config{DataDir: dir, AssistantAgentID: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(st, client, nil, manager)
	ctx := context.Background()
	if _, err := manager.Inbound(ctx, io.InboundMessage{Channel: io.ChannelWeb, Sender: "owner", Text: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Inbound(ctx, io.InboundMessage{Channel: io.ChannelWeb, Sender: "owner", Text: "second"}); err != nil {
		t.Fatal(err)
	}
	convID := st.PrimaryConversationID()
	if err := h.reconcileRun(ctx, convID, "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DrainPending(ctx, convID); err != nil {
		t.Fatal(err)
	}
	if err := h.reconcileRun(ctx, convID, "run-1"); err != nil {
		t.Fatal(err)
	}
	conv, err := st.GetConversation(convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 4 || conv.Messages[2].Content != "answer to first" || conv.Messages[3].Content != "answer to second" || conv.Messages[2].TurnID == conv.Messages[3].TurnID || conv.ActiveRunID != "" {
		t.Fatalf("reconciled conversation: %+v", conv)
	}
}

type flakyMatrixDelivery struct {
	store   *store.Store
	turnIDs []string
	failed  bool
}

func (d *flakyMatrixDelivery) Type() io.ChannelType { return io.ChannelMatrix }

func (d *flakyMatrixDelivery) Send(_ context.Context, msg io.OutboundMessage) error {
	d.turnIDs = append(d.turnIDs, msg.TurnID)
	if !d.failed {
		d.failed = true
		return errors.New("response lost after external acceptance")
	}
	_, _, err := d.store.AppendAssistantForTurn(msg.ConversationID, "", msg.TurnID, msg.Text, "matrix", "eve")
	return err
}

func TestExternalDeliveryRetryKeepsTurnUntilAcknowledged(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "identities.json"), []byte(`{"identities":[{"name":"owner","owner":true,"channels":[{"type":"matrix","address":"@owner:test"}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	var inputID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/runs"):
			json.NewEncoder(w).Encode(map[string]string{"run_id": "run-1"})
		case strings.HasSuffix(r.URL.Path, "/inputs"):
			var input struct {
				InputID string `json:"input_id"`
			}
			json.NewDecoder(r.Body).Decode(&input)
			inputID = input.InputID
			json.NewEncoder(w).Encode(map[string]string{"run_id": "run-1"})
		default:
			json.NewEncoder(w).Encode(map[string]any{"status": "waiting", "persistent": true, "last_input_id": inputID, "response": "the reply"})
		}
	}))
	defer server.Close()
	client, err := agentfoundry.NewClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := io.NewManager(st, client, io.Config{DataDir: dir, AssistantAgentID: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	manager.Reg.Register(io.Channel{ID: "matrix", Type: io.ChannelMatrix, Input: true, Output: true, DefaultRecipient: "!room:test"})
	adapter := &flakyMatrixDelivery{store: st}
	manager.Router.RegisterAdapter(adapter)
	h := NewHandler(st, client, nil, manager)
	ctx := context.Background()
	if _, err := manager.Inbound(ctx, io.InboundMessage{Channel: io.ChannelMatrix, Sender: "@owner:test", Text: "hello", ThreadRef: "!room:test"}); err != nil {
		t.Fatal(err)
	}
	convID := st.PrimaryConversationID()
	if err := h.reconcileRun(ctx, convID, "run-1"); err == nil {
		t.Fatal("expected transient delivery error")
	}
	conv, err := st.GetConversation(convID)
	if err != nil {
		t.Fatal(err)
	}
	if conv.ActiveRunID != "run-1" || conv.DeliveryFailures != 1 || len(conv.Messages) != 1 {
		t.Fatalf("reply lost after ambiguous delivery: %+v", conv)
	}
	reloaded, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.DeliveryReady(convID, "run-1") {
		t.Fatal("delivery backoff was not restored after restart")
	}
	if err := h.reconcileRun(ctx, convID, "run-1"); err != nil || len(adapter.turnIDs) != 1 {
		t.Fatalf("delivery ignored retry backoff: %v; attempts=%v", err, adapter.turnIDs)
	}
	time.Sleep(2100 * time.Millisecond)
	if err := h.reconcileRun(ctx, convID, "run-1"); err != nil {
		t.Fatal(err)
	}
	conv, err = st.GetConversation(convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(adapter.turnIDs) != 2 || adapter.turnIDs[0] == "" || adapter.turnIDs[0] != adapter.turnIDs[1] || len(conv.Messages) != 2 || conv.ActiveRunID != "" || conv.DeliveryFailures != 0 {
		t.Fatalf("delivery was not retried idempotently: turns=%v conversation=%+v", adapter.turnIDs, conv)
	}
}

func TestUncertainInputSignalRetriesSameTurn(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	var acceptedID string
	submissions := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/runs"):
			json.NewEncoder(w).Encode(map[string]string{"run_id": "run-1"})
		case strings.HasSuffix(r.URL.Path, "/inputs"):
			submissions++
			var input struct {
				InputID string `json:"input_id"`
			}
			json.NewDecoder(r.Body).Decode(&input)
			if acceptedID != "" && acceptedID != input.InputID {
				t.Errorf("retry changed input identity: %s -> %s", acceptedID, input.InputID)
			}
			acceptedID = input.InputID
			if submissions == 1 {
				http.Error(w, "connection dropped after acceptance", http.StatusBadGateway)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"run_id": "run-1"})
		case r.Method == http.MethodGet:
			if submissions == 1 {
				json.NewEncoder(w).Encode(map[string]any{"status": "running", "persistent": true, "current_input_id": acceptedID})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"status": "waiting", "persistent": true, "last_input_id": acceptedID, "response": "answer"})
			}
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := agentfoundry.NewClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := io.NewManager(st, client, io.Config{DataDir: dir, AssistantAgentID: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(st, client, nil, manager)
	ctx := context.Background()
	if _, err := manager.Inbound(ctx, io.InboundMessage{Channel: io.ChannelWeb, Sender: "owner", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	convID := st.PrimaryConversationID()
	if submissions != 1 {
		t.Fatalf("initial submission count: %d", submissions)
	}
	if err := h.reconcileRun(ctx, convID, "run-1"); err != nil {
		t.Fatal(err)
	}
	if submissions != 2 {
		t.Fatalf("uncertain submission was not retried: %d", submissions)
	}
	if err := h.reconcileRun(ctx, convID, "run-1"); err != nil {
		t.Fatal(err)
	}
	conv, err := st.GetConversation(convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 2 || conv.Messages[1].Content != "answer" || conv.ActiveRunID != "" {
		t.Fatalf("turn did not finish after retry: %+v", conv)
	}
}
