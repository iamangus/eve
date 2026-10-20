package context

import (
	"context"
	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestForceCompactionDoesNotSummarizeProtectedConversation(t *testing.T) {
	m := NewManager(nil, nil, Config{ProtectedTailTokens: 12000, ChunkTokens: 20000})
	if chunk := m.selectChunk([]store.Message{{ID: 1, Content: "short conversation"}}); len(chunk) != 0 {
		t.Fatal("protected tail summarized")
	}
}

func TestHistorianLoopCancelsAndJoinsItsWorker(t *testing.T) {
	st, _ := store.New(t.TempDir())
	conv, _ := st.CreateConversation("chat")
	st.AppendUserMessage(conv.ID, strings.Repeat("x", 100), "web", "owner")
	st.AppendUserMessage(conv.ID, strings.Repeat("x", 100), "web", "owner")
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	client, _ := agentfoundry.NewClient(server.URL, "key")
	m := NewManager(st, client, Config{AgentID: "historian", BudgetTokens: 20, TriggerFraction: 0.5, ProtectedTailTokens: 10, ChunkTokens: 100})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); m.Loop(ctx) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("historian never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("historian failed to join on shutdown")
	}
	if m.running.Load() {
		t.Fatal("loop exited with a detached historian")
	}
}

func TestHistorianRejectsPartialMultiCompartmentSummary(t *testing.T) {
	_, err := ParseManifest(`{"compartments":[{"p1":"a","p2":"b","p3":"c","p4":"d"},{"p1":"incomplete"}]}`)
	if err == nil {
		t.Fatal("partial multi-compartment output accepted for the full chunk")
	}
}

func TestCuratingStaleMemorySnapshotDoesNotDiscardNewFacts(t *testing.T) {
	st, _ := store.New(t.TempDir())
	st.AddMemory(store.Memory{Content: "first", Hash: "one"})
	snapshot := st.Memories()
	st.AddMemory(store.Memory{Content: "second", Hash: "two"})
	if err := st.CurateMemories(snapshot, nil); err != nil {
		t.Fatal(err)
	}
	if len(st.Memories()) != 2 {
		t.Fatal("concurrent promotion was lost")
	}
}

func TestEmergencyHistoryKeepsProvenanceAndReportsRenderedTokens(t *testing.T) {
	st, _ := store.New(t.TempDir())
	conv, _ := st.CreateConversation("chat")
	st.AppendUserMessage(conv.ID, strings.Repeat("old ", 50), "web", "owner")
	st.AppendUserMessage(conv.ID, strings.Repeat("new ", 20), "email", "third-party")
	m := NewManager(st, nil, Config{AgentID: "historian", BudgetTokens: 30, ProtectedTailTokens: 10})
	history, stats, err := m.RenderHistory(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stats.Truncated {
		t.Fatal("test did not trigger emergency truncation")
	}
	tokens := 0
	for _, msg := range history {
		tokens += EstimateTokens(msg.Content.(string))
	}
	if tokens != stats.RenderedTokens {
		t.Fatalf("reported %d tokens, actually rendered %d", stats.RenderedTokens, tokens)
	}
	if !strings.Contains(history[len(history)-1].Content.(string), "sender: third-party") {
		t.Fatal("emergency path discarded sender provenance")
	}
}
