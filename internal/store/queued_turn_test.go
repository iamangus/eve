package store

import (
	"errors"
	"testing"
)

func TestQueuedTurnsAndReplyDedupSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "first", "email", "owner", "thread-1", "owner@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "second", "web", "owner", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimPendingOwnerMessage(c.ID, "run-1", first.ID)
	if err != nil || !claimed {
		t.Fatalf("first claim: %t, %v", claimed, err)
	}
	s, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	channel, thread, to, err := s.ActiveRunReply(c.ID, "run-1")
	if err != nil || channel != "email" || thread != "thread-1" || to != "owner@example.com" {
		t.Fatalf("reply: %q %q %q, %v", channel, thread, to, err)
	}
	if m, err := s.PendingOwnerMessage(c.ID); err != nil || m != nil {
		t.Fatalf("queued turn started early: %+v, %v", m, err)
	}
	if _, created, err := s.AppendAssistantForTurn(c.ID, "run-1", "chat-1", "answer", "email", "eve"); err != nil || !created {
		t.Fatalf("first answer: %t %v", created, err)
	}
	if _, created, err := s.AppendAssistantForTurn(c.ID, "run-1", "chat-1", "answer", "email", "eve"); err != nil || created {
		t.Fatalf("duplicate answer: %t %v", created, err)
	}
	if err := s.ClearActiveRunID(c.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	if m, err := s.PendingOwnerMessage(c.ID); err != nil || m == nil || m.ID != second.ID {
		t.Fatalf("next queued turn: %+v, %v", m, err)
	}
}

func TestRepeatedSourceMessageReusesQueuedTurn(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	first, created, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "hello", "matrix", "owner", "!room", "@owner", "event-1")
	if err != nil || !created {
		t.Fatalf("first: %+v %t %v", first, created, err)
	}
	again, created, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "hello", "matrix", "owner", "!room", "@owner", "event-1")
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("duplicate: %+v %t %v", again, created, err)
	}
	if _, _, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "different message", "matrix", "owner", "!room", "@owner", "event-1"); !errors.Is(err, ErrSourceConflict) {
		t.Fatalf("conflicting source ID was reused: %v", err)
	}
	conv, err := s.GetConversation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 1 || len(conv.PendingMessageIDs) != 1 {
		t.Fatalf("duplicate queued input: %+v", conv)
	}
}

func TestConversationDeletionBlocksNewTurnsWhileRunsStop(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "first", "web", "owner", "", "", "first-id")
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimPendingOwnerMessage(c.ID, "run-1", first.ID); err != nil || !claimed {
		t.Fatalf("claim: %v, %v", claimed, err)
	}
	runs, err := s.BeginConversationDelete(c.ID)
	if err != nil || len(runs) != 1 || runs[0] != "run-1" {
		t.Fatalf("run snapshot: %v, %v", runs, err)
	}
	if _, _, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "second", "web", "owner", "", "", "second-id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting conversation accepted a turn: %v", err)
	}
	if s.PrimaryConversationID() != "" || s.DeliveryReady(c.ID, "run-1") || len(s.PendingOwnerConversations()) != 0 {
		t.Fatal("deleting conversation remained eligible for work")
	}
	if err := s.DeleteConversation(c.ID); err != nil {
		t.Fatal(err)
	}
	s.EndConversationDelete(c.ID)
	if _, err := s.GetConversation(c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted conversation remained visible: %v", err)
	}
}
