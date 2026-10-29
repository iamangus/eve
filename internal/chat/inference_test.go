package chat

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/store"
)

func TestMessageInferenceUsesStoredCorrelation(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	msg, _, err := st.AppendAssistantForTurn(conv.ID, "run", "turn", "hello", "web", "eve")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer key" || r.URL.Path != "/api/v1/runs/run/inference" || r.URL.Query().Get("input_id") != "turn" || r.URL.Query().Get("cursor") != "next" {
			t.Errorf("bad upstream request %s", r.URL)
		}
		fmt.Fprint(w, `{"attempts":[],"next_cursor":"after"}`)
	}))
	defer server.Close()
	client, err := agentfoundry.NewClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(st, client, nil, nil)
	for _, tc := range []struct {
		conversation, message string
		status                int
	}{{conv.ID, strconv.FormatInt(msg.ID, 10), 200}, {conv.ID, "999", 404}, {"missing", strconv.FormatInt(msg.ID, 10), 404}} {
		r := httptest.NewRequest("GET", "/?cursor=next&run_id=attacker&input_id=attacker", nil)
		r.SetPathValue("id", tc.conversation)
		r.SetPathValue("message", tc.message)
		w := httptest.NewRecorder()
		h.messageInference(w, r)
		if w.Code != tc.status {
			t.Fatalf("unexpected status %d: %s", w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("unexpected upstream access: %d", calls)
	}
}
