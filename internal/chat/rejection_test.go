package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/io"
	"github.com/iamangus/eve/internal/store"
)

func TestRejectedPersistentInputDoesNotBlockConversationForever(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.New(dir)
	conv, _ := st.CreateConversation("chat")
	msg, _, _ := st.AppendQueuedOwnerMessageWithSource(conv.ID, "rejected input", "web", "owner", "", "", "message1")
	st.ClaimPendingOwnerMessage(conv.ID, "run1", msg.ID)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"status": "waiting", "persistent": true, "last_input_id": "older"})
			return
		}
		http.Error(w, "invalid input", http.StatusBadRequest)
	}))
	defer server.Close()
	client, _ := agentfoundry.NewClient(server.URL, "key")
	manager, _ := io.NewManager(st, client, io.Config{DataDir: dir, AssistantAgentID: "agent"})
	h := NewHandler(st, client, nil, manager)
	if err := h.reconcileRun(context.Background(), conv.ID, "run1"); err != nil {
		t.Fatal(err)
	}
	full, _ := st.GetConversation(conv.ID)
	if full.ActiveRunID != "" || len(full.Messages) != 2 || !strings.Contains(full.Messages[1].Content, "input rejected") {
		t.Fatal("permanently rejected input left the conversation blocked")
	}
}
