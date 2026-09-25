package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConversationAndRunWritesRollbackOnFailure(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "conversations.json.tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "hello", "web", "owner", "", "", "client-1"); err == nil {
		t.Fatal("append succeeded without durable write")
	}
	conv, err := s.GetConversation(c.ID)
	if err != nil || len(conv.Messages) != 0 || len(conv.PendingMessageIDs) != 0 {
		t.Fatalf("failed append remained in memory: %+v %v", conv, err)
	}
	if err := os.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	msg, created, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "hello", "web", "owner", "", "", "client-1")
	if err != nil || !created {
		t.Fatalf("retry append: %+v %t %v", msg, created, err)
	}
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimPendingOwnerMessage(c.ID, "run-1", msg.ID); err == nil || claimed {
		t.Fatalf("claim succeeded without persistence: %t %v", claimed, err)
	}
	if pending, err := s.PendingOwnerMessage(c.ID); err != nil || pending == nil || pending.ID != msg.ID {
		t.Fatalf("failed claim removed pending turn: %+v %v", pending, err)
	}
	if err := os.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimPendingOwnerMessage(c.ID, "run-1", msg.ID); err != nil || !claimed {
		t.Fatalf("retry claim: %t %v", claimed, err)
	}
	runTmp := filepath.Join(dir, "persistent_runs.json.tmp")
	if err := os.Mkdir(runTmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPersistentRun("frontend:chat", "run-1"); err == nil || s.PersistentRun("frontend:chat") != "" {
		t.Fatalf("failed run ID persisted in memory: %v", err)
	}
	if err := os.Remove(runTmp); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPersistentRun("frontend:chat", "run-1"); err != nil {
		t.Fatal(err)
	}
}

func TestCompartmentAndMemoryWritesRollbackOnFailure(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	compartmentTmp := filepath.Join(dir, "compartments.json.tmp")
	if err := os.Mkdir(compartmentTmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCompartment(c.ID, Compartment{StartMsgID: 1, EndMsgID: 2}); err == nil || len(s.Compartments(c.ID)) != 0 {
		t.Fatalf("failed compartment was retained: %v", err)
	}
	if err := os.Remove(compartmentTmp); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCompartment(c.ID, Compartment{StartMsgID: 1, EndMsgID: 2}); err != nil {
		t.Fatal(err)
	}
	memoryTmp := filepath.Join(dir, "memories.json.tmp")
	if err := os.Mkdir(memoryTmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMemory(Memory{Content: "fact", Hash: "hash-1"}); err == nil || len(s.Memories()) != 0 {
		t.Fatalf("failed memory was retained: %v", err)
	}
	if err := s.ReplaceMemories([]Memory{{Content: "fact", Hash: "hash-2"}}); err == nil || len(s.Memories()) != 0 {
		t.Fatalf("failed memory replacement was retained: %v", err)
	}
	if err := os.Remove(memoryTmp); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMemory(Memory{Content: "fact", Hash: "hash-1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteConversation(c.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.Compartments(c.ID)) != 0 {
		t.Fatal("deleted conversation kept its compartments")
	}
	s, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Compartments(c.ID)) != 0 {
		t.Fatal("deleted conversation compartments survived restart")
	}
	if _, err := s.AddCompartment(c.ID, Compartment{}); err != ErrNotFound {
		t.Fatalf("orphan compartment accepted: %v", err)
	}
}
