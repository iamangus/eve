package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeletionFenceAndDerivedMemoryCleanupSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	st, _ := New(dir)
	conv, _ := st.CreateConversation("chat")
	comp, _ := st.AddCompartment(conv.ID, Compartment{StartMsgID: 1, EndMsgID: 2})
	if err := st.AddMemory(Memory{Content: "private fact", SourceCompartment: comp.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BeginConversationDelete(conv.ID); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.DeleteRequests()) != 1 {
		t.Fatal("delete request not durable")
	}
	if _, _, err := reloaded.AppendQueuedOwnerMessageWithSource(conv.ID, "late", "web", "owner", "", "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("pending deletion accepted input")
	}
	if err := reloaded.DeleteConversation(conv.ID); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.FinishDelete(conv.ID); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Memories()) != 0 || len(reloaded.Compartments(conv.ID)) != 0 {
		t.Fatal("derived context survived deletion")
	}
}

func TestDeletionResumesAfterConversationFileWasRemovedBeforeContextWrite(t *testing.T) {
	dir := t.TempDir()
	st, _ := New(dir)
	conv, _ := st.CreateConversation("chat")
	comp, _ := st.AddCompartment(conv.ID, Compartment{StartMsgID: 1, EndMsgID: 2})
	st.AddMemory(Memory{Content: "derived", SourceCompartment: comp.ID})
	st.BeginConversationDelete(conv.ID)
	path := filepath.Join(dir, "compartments.json.tmp")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteConversation(conv.ID); err == nil {
		t.Fatal("expected partial deletion failure")
	}
	st, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.DeleteRequests()) != 1 {
		t.Fatal("partial operation lost its journal")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteConversation(conv.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishDelete(conv.ID); err != nil {
		t.Fatal(err)
	}
	if len(st.Memories()) != 0 || len(st.Compartments(conv.ID)) != 0 {
		t.Fatal("partial deletion left derived data")
	}
}
