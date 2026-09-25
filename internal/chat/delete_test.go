package chat

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/store"
)

func TestDeleteConversationStopsScopedRunsAndClearsContext(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddCompartment(conv.ID, store.Compartment{StartMsgID: 1, EndMsgID: 2}); err != nil {
		t.Fatal(err)
	}
	for scope, runID := range map[string]string{"frontend-v2:": "visible", "frontend-events:": "hidden"} {
		if err := st.SetPersistentRun(scope+conv.ID, runID); err != nil {
			t.Fatal(err)
		}
	}
	stopped := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/cancel") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		stopped[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/runs/"), "/cancel")] = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := agentfoundry.NewClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(st, client, nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/conversations/"+conv.ID, nil)
	req.SetPathValue("id", conv.ID)
	response := httptest.NewRecorder()
	h.deleteConversation(response, req)
	if response.Code != http.StatusNoContent || !stopped["visible"] || !stopped["hidden"] {
		t.Fatalf("delete did not cancel both runs: status=%d, stopped=%v", response.Code, stopped)
	}
	if len(st.Compartments(conv.ID)) != 0 || st.PersistentRun("frontend-v2:"+conv.ID) != "" || st.PersistentRun("frontend-events:"+conv.ID) != "" {
		t.Fatal("deleted conversation left context or persistent run IDs")
	}
}

func TestDeleteConversationGatesInboundWhileCancelPending(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPersistentRun("frontend-v2:"+conv.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := agentfoundry.NewClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(st, client, nil, nil)
	finished := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodDelete, "/api/conversations/"+conv.ID, nil)
		req.SetPathValue("id", conv.ID)
		response := httptest.NewRecorder()
		h.deleteConversation(response, req)
		finished <- response.Code
	}()
	<-started
	if _, _, err := st.AppendQueuedOwnerMessageWithSource(conv.ID, "late", "web", "owner", "", "", "late-id"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("turn entered a conversation being deleted: %v", err)
	}
	close(release)
	if code := <-finished; code != http.StatusNoContent {
		t.Fatalf("delete returned %d", code)
	}
}
