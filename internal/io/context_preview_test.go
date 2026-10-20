package io

import (
	"strings"
	"testing"

	ctxmgr "github.com/iamangus/eve/internal/context"
	"github.com/iamangus/eve/internal/store"
)

func TestBootstrapPreviewReportsFinalBoundedHistory(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.New(dir)
	conv, _ := st.CreateConversation("chat")
	for i := 0; i < 130; i++ {
		if err := st.AppendUserMessage(conv.ID, strings.Repeat("x", 40), "web", "owner"); err != nil {
			t.Fatal(err)
		}
	}
	cm := ctxmgr.NewManager(st, nil, ctxmgr.Config{BudgetTokens: 64000})
	manager, _ := NewManager(st, nil, Config{DataDir: dir})
	manager.SetContext(cm)
	history, err := manager.BootstrapHistory(conv.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := cm.Snapshot()
	tokens := 0
	for _, msg := range history {
		tokens += ctxmgr.EstimateTokens(msg.Content.(string))
	}
	if !snapshot.BootstrapPreviewAvailable || snapshot.BootstrapPreviewMessages != len(history) || snapshot.BootstrapPreviewTokens != tokens {
		t.Fatal("preview does not report final bounded submission")
	}
	if len(history) >= 130 {
		t.Fatal("test did not exercise message count limit")
	}
}
