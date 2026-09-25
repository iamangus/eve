package io

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/store"
)

func TestInboundSendsCurrentMessageOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendAssistantMessage(conv.ID, "", "previous reply", "web", "eve"); err != nil {
		t.Fatal(err)
	}
	var submitted struct {
		Message string                 `json:"message"`
		History []agentfoundry.Message `json:"history"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/runs") {
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Error(err)
			}
		} else if strings.HasSuffix(r.URL.Path, "/inputs") {
			var input struct {
				Message string `json:"message"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			submitted.Message = input.Message
		} else {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{"run_id":"run-1"}`))
	}))
	defer server.Close()
	client, err := agentfoundry.NewClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(st, client, Config{DataDir: dir, AssistantAgentID: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Inbound(context.Background(), InboundMessage{Channel: ChannelWeb, Sender: "owner", Text: "new question"}); err != nil {
		t.Fatal(err)
	}
	if submitted.Message != "new question" || len(submitted.History) != 1 || submitted.History[0].Content != "previous reply" {
		t.Fatalf("duplicated current message in context: %+v", submitted)
	}
}

func TestInboundIdentityUsesChannelAddress(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := LoadResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Resolve(ChannelSMS, "owner"); got.Owner {
		t.Fatal("a sender name on an unconfigured channel became the owner")
	}
	if got := r.Resolve(ChannelWeb, "owner"); !got.Owner {
		t.Fatal("configured owner web address was not recognized")
	}
	submissions := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		submissions++
		w.Write([]byte(`{"run_id":"run-1"}`))
	}))
	defer server.Close()
	client, err := agentfoundry.NewClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(st, client, Config{DataDir: dir, AssistantAgentID: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	post := func(payload string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/inbound/sms", bytes.NewBufferString(payload))
		req.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		manager.webhookInbound(ChannelSMS, "secret")(w, req)
		if w.Code != http.StatusAccepted {
			t.Fatalf("webhook returned %d: %s", w.Code, w.Body.String())
		}
	}
	post(`{"sender":"owner","text":"claimed owner"}`)
	if submissions != 0 {
		t.Fatalf("unconfigured sender triggered %d agent requests", submissions)
	}
	post(`{"text":"trusted device default"}`)
	if submissions != 2 {
		t.Fatalf("trusted webhook default did not submit a turn: %d requests", submissions)
	}
}

func TestQueuedChannelTurnsResumeAfterRestart(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var inputs []string
	created := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/runs") {
			created++
		} else if strings.HasSuffix(r.URL.Path, "/inputs") {
			var input struct {
				Message string `json:"message"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			inputs = append(inputs, input.Message)
		}
		w.Write([]byte(`{"run_id":"persistent-1"}`))
	}))
	defer server.Close()
	client, err := agentfoundry.NewClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(st, client, Config{DataDir: dir, AssistantAgentID: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if id, err := manager.Inbound(ctx, InboundMessage{Channel: ChannelWeb, Sender: "owner", Text: "first"}); err != nil || id != "persistent-1" {
		t.Fatalf("first input: %q, %v", id, err)
	}
	if id, err := manager.Inbound(ctx, InboundMessage{Channel: ChannelWeb, Sender: "owner", Text: "second"}); err != nil || id != "" {
		t.Fatalf("queued input: %q, %v", id, err)
	}
	mu.Lock()
	if created != 1 || len(inputs) != 1 {
		t.Fatalf("before restart: created=%d inputs=%v", created, inputs)
	}
	mu.Unlock()
	restored, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	convID := restored.PrimaryConversationID()
	if err := restored.ClearActiveRunID(convID, "persistent-1"); err != nil {
		t.Fatal(err)
	}
	manager, err = NewManager(restored, client, Config{DataDir: dir, AssistantAgentID: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := manager.DrainPending(ctx, convID); err != nil || id != "persistent-1" {
		t.Fatalf("restored input: %q, %v", id, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if created != 1 || len(inputs) != 2 || inputs[0] != "first" || inputs[1] != "second" {
		t.Fatalf("after restart: created=%d inputs=%v", created, inputs)
	}
}

func TestLegacyRunMigrationWaitsUntilIdle(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPersistentRun("frontend:"+conv.ID, "legacy-run"); err != nil {
		t.Fatal(err)
	}
	status := "running"
	cancels := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]string{"status": status})
			return
		}
		cancels++
	}))
	defer server.Close()
	client, err := agentfoundry.NewClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(st, client, Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	manager.MigrateLegacyRuns(context.Background())
	if cancels != 0 || st.PersistentRun("frontend:"+conv.ID) == "" {
		t.Fatal("active legacy workflow was canceled")
	}
	status = "waiting"
	manager.MigrateLegacyRuns(context.Background())
	if cancels != 1 || st.PersistentRun("frontend:"+conv.ID) != "" {
		t.Fatal("idle legacy workflow was not retired")
	}
}
