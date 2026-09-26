package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/io"
	"github.com/iamangus/eve/internal/store"
)

func postMessage(t *testing.T, h *Handler, convID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/conversations/"+convID+"/messages", strings.NewReader(body))
	req.SetPathValue("id", convID)
	rec := httptest.NewRecorder()
	h.sendMessage(rec, req)
	return rec
}

func TestSendMessageSteersActiveWebTurn(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var lastInput struct {
		InputID  string            `json:"input_id"`
		Message  string            `json:"message"`
		Metadata map[string]string `json:"metadata"`
	}
	submissions := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/inputs") {
			var in struct {
				InputID  string            `json:"input_id"`
				Message  string            `json:"message"`
				Metadata map[string]string `json:"metadata"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			lastInput = in
			submissions++
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]string{"run_id": "run-1"})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
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

	if _, _, err := st.AppendQueuedOwnerMessageWithSource(conv.ID, "first", "web", "owner", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimPendingOwnerMessage(conv.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkActiveSubmitted(conv.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}

	rec := postMessage(t, h, conv.ID, `{"content":"actually stop","client_id":"c2"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("steer status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp sendMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Steered || resp.Queued || resp.RunID != "run-1" || resp.MessageID != 2 {
		t.Fatalf("steer response: %+v", resp)
	}
	mu.Lock()
	defer mu.Unlock()
	if submissions != 1 {
		t.Fatalf("submission count: %d", submissions)
	}
	if lastInput.InputID != "chat-"+conv.ID+"-2" || lastInput.Metadata["source"] != "web" || lastInput.Message != "actually stop" {
		t.Fatalf("injected input: %+v", lastInput)
	}
	if ids := st.PendingOwnerConversations(); len(ids) != 0 {
		t.Fatalf("steered message left a pending turn: %v", ids)
	}
}

func TestSendMessageQueuesWhenInjectionFails(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/inputs") {
			http.Error(w, "active turn no longer accepts steering", http.StatusConflict)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
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

	if _, _, err := st.AppendQueuedOwnerMessageWithSource(conv.ID, "first", "web", "owner", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimPendingOwnerMessage(conv.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkActiveSubmitted(conv.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}

	rec := postMessage(t, h, conv.ID, `{"content":"follow-up","client_id":"c2"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("fallback status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp sendMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Steered || !resp.Queued || resp.RunID != "" || resp.MessageID != 2 {
		t.Fatalf("fallback response: %+v", resp)
	}
	convFull, err := st.GetConversation(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(convFull.PendingMessageIDs) != 1 || convFull.PendingMessageIDs[0] != 2 {
		t.Fatalf("fallback message not queued: %v", convFull.PendingMessageIDs)
	}
}

func TestSteeringLostResponseSurvivesRestartAndDoesNotStartAnotherTurn(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AppendQueuedOwnerMessageWithSource(conv.ID, "first", "web", "owner", "", "", "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimPendingOwnerMessage(conv.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkActiveSubmitted(conv.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	submissions := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/inputs") {
			submissions++
			var input struct {
				InputID string `json:"input_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.InputID != "chat-"+conv.ID+"-2" {
				t.Errorf("retry changed input identity: %q", input.InputID)
			}
			if submissions == 1 {
				http.Error(w, "response lost after acceptance", http.StatusBadGateway)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"run_id": "run-1"})
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/run-1") {
			if submissions < 2 {
				json.NewEncoder(w).Encode(map[string]any{"status": "running", "persistent": true, "current_input_id": "chat-" + conv.ID + "-1"})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"status": "waiting", "persistent": true, "last_input_id": "chat-" + conv.ID + "-1", "response": "revised answer"})
			}
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
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
	result := postMessage(t, h, conv.ID, `{"content":"actually stop","client_id":"c2"}`)
	if result.Code != http.StatusAccepted {
		t.Fatalf("steering response: %d %s", result.Code, result.Body.String())
	}
	var response sendMessageResponse
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Queued || response.Steered || len(st.PendingSteersForRun(conv.ID, "run-1")) != 1 {
		t.Fatalf("uncertain steering was not retained: %+v", response)
	}
	restarted, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	manager, err = io.NewManager(restarted, client, io.Config{DataDir: dir, AssistantAgentID: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	h = NewHandler(restarted, client, nil, manager)
	if err := h.reconcileRun(t.Context(), conv.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	if !restarted.ConfirmedSteer(conv.ID, "run-1", 2) {
		t.Fatal("reconciliation did not confirm the accepted steering update")
	}
	if err := h.reconcileRun(t.Context(), conv.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	full, err := restarted.GetConversation(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if submissions != 2 || len(full.Messages) != 3 || full.Messages[2].Content != "revised answer" || len(full.PendingMessageIDs) != 0 || full.ActiveRunID != "" {
		t.Fatalf("steering retry created a second turn: submissions=%d conversation=%+v", submissions, full)
	}
}

func TestReconcileDeliversTurnFinishedBySteeredInput(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AppendQueuedOwnerMessageWithSource(conv.ID, "first", "web", "owner", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimPendingOwnerMessage(conv.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkActiveSubmitted(conv.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	msg, _, _, err := st.AppendSteeredOwnerMessageWithSource(conv.ID, "follow-up", "web", "owner", "", "", "c2", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ConfirmSteer(conv.ID, "run-1", msg.ID); err != nil {
		t.Fatal(err)
	}

	submissions := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/inputs") {
			submissions++
			t.Error("steered turn completion resubmitted the active input")
			json.NewEncoder(w).Encode(map[string]string{"run_id": "run-1"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"status": "waiting", "persistent": true,
			"last_input_id": "chat-" + conv.ID + "-1", "response": "combined answer",
		})
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

	if err := h.reconcileRun(t.Context(), conv.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	convFull, err := st.GetConversation(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(convFull.Messages) != 3 || convFull.Messages[2].Role != "assistant" || convFull.Messages[2].Content != "combined answer" || convFull.ActiveRunID != "" {
		t.Fatalf("steered turn was not delivered: %+v", convFull)
	}
	if submissions != 0 {
		t.Fatalf("unexpected input submissions: %d", submissions)
	}
}

func TestReconcileDoesNotMistakeQueuedMessageForActiveTurnResult(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AppendQueuedOwnerMessageWithSource(conv.ID, "first", "web", "owner", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimPendingOwnerMessage(conv.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkActiveSubmitted(conv.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AppendQueuedOwnerMessageWithSource(conv.ID, "second", "web", "owner", "", "", ""); err != nil {
		t.Fatal(err)
	}
	submittedID := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/inputs") {
			var in struct {
				InputID string `json:"input_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			submittedID = in.InputID
			json.NewEncoder(w).Encode(map[string]string{"run_id": "run-1"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"status": "waiting", "persistent": true,
			"last_input_id": "chat-" + conv.ID + "-2", "response": "wrong turn",
		})
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
	if err := h.reconcileRun(t.Context(), conv.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	full, err := st.GetConversation(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if submittedID != "chat-"+conv.ID+"-1" || full.ActiveRunID != "run-1" || len(full.Messages) != 2 {
		t.Fatalf("queued message was treated as an active result: submitted=%q conversation=%+v", submittedID, full)
	}
}
