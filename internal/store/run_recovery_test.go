package store

import "testing"

func TestRestartRepairsInterruptedPersistentRunReplacement(t *testing.T) {
	dir := t.TempDir()
	st, _ := New(dir)
	conv, _ := st.CreateConversation("chat")
	msg, _, _ := st.AppendQueuedOwnerMessageWithSource(conv.ID, "work", "web", "owner", "", "", "id")
	st.SetPersistentRun("frontend-v2:"+conv.ID, "old")
	st.ClaimPendingOwnerMessage(conv.ID, "old", msg.ID)
	if err := st.ReplaceActiveOwnerRun(conv.ID, "old", "replacement", msg.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.PersistentRun("frontend-v2:"+conv.ID) != "replacement" {
		t.Fatal("restart restored stale persistent mapping")
	}
}
