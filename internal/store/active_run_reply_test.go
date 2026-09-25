package store

import "testing"

func TestActiveRunReplyStaysBoundToOriginMessage(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	origin, err := s.AppendUserMessageReturn(c.ID, "first", "matrix", "owner", "room-1", "@owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetActiveRunForMessage(c.ID, "run-1", origin.ID); err != nil {
		t.Fatal(err)
	}
	second, err := s.AppendUserMessageReturn(c.ID, "second", "email", "owner", "thread-2", "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetActiveRunForMessage(c.ID, "run-2", second.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	channel, thread, target, err := restored.ActiveRunReply(c.ID, "run-1")
	if err != nil || channel != "matrix" || thread != "room-1" || target != "@owner" {
		t.Fatalf("reply = %q %q %q, %v", channel, thread, target, err)
	}
	runs, err := restored.ActiveRuns()
	if err != nil || len(runs) != 2 {
		t.Fatalf("active runs = %+v, %v", runs, err)
	}
	if err := restored.ClearActiveRunID(c.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := restored.ActiveRunReply(c.ID, "run-1"); err == nil {
		t.Fatal("cleared run still has reply destination")
	}
	channel, thread, target, err = restored.ActiveRunReply(c.ID, "run-2")
	if err != nil || channel != "email" || thread != "thread-2" || target != "owner@example.com" {
		t.Fatalf("second reply = %q %q %q, %v", channel, thread, target, err)
	}
}
