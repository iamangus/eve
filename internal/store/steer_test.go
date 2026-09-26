package store

import "testing"

func TestSteeredInputIsDurableUntilConfirmedOrQueued(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "first", "web", "owner", "", "", "initial"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimPendingOwnerMessage(c.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkActiveSubmitted(c.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	first, created, target, err := s.AppendSteeredOwnerMessageWithSource(c.ID, "follow-up", "web", "owner", "", "", "client-1", "run-1")
	if err != nil || !created || target != "run-1" {
		t.Fatalf("append steering input: created=%v target=%s err=%v", created, target, err)
	}
	if err := s.ClearActiveRunID(c.ID, "run-1"); err != ErrSteerPending {
		t.Fatalf("unconfirmed input was abandoned: %v", err)
	}
	reloaded, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if steers := reloaded.PendingSteersForRun(c.ID, "run-1"); len(steers) != 1 || steers[0].MessageID != first.ID || steers[0].AnchorID != 1 {
		t.Fatalf("pending steering lost across restart: %+v", steers)
	}
	duplicate, created, target, err := reloaded.AppendSteeredOwnerMessageWithSource(c.ID, "follow-up", "web", "owner", "", "", "client-1", "run-1")
	if err != nil || created || target != "run-1" || duplicate.ID != first.ID {
		t.Fatalf("retry changed steering identity: %+v, %v, %q, %v", duplicate, created, target, err)
	}
	if err := reloaded.ConfirmSteer(c.ID, "run-1", first.ID); err != nil {
		t.Fatal(err)
	}
	if !reloaded.ConfirmedSteer(c.ID, "run-1", first.ID) {
		t.Fatal("confirmed steering not recorded")
	}
	conv, err := reloaded.GetConversation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.PendingMessageIDs) != 0 || len(conv.SteeredMessageIDs) != 1 || conv.SteeredMessageIDs[0] != first.ID {
		t.Fatalf("steered message shown as pending: %+v", conv)
	}
	if err := reloaded.ClearActiveRunID(c.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	if reloaded.ConfirmedSteer(c.ID, "run-1", first.ID) {
		t.Fatal("finished turn retained steering metadata")
	}
}

func TestRejectedSteerQueuesInMessageOrder(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "initial", "web", "owner", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimPendingOwnerMessage(c.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkActiveSubmitted(c.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	steered, _, _, err := s.AppendSteeredOwnerMessageWithSource(c.ID, "second", "web", "owner", "", "", "second", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "third", "web", "owner", "", "", "third"); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueSteer(c.ID, "run-1", steered.ID); err != nil {
		t.Fatal(err)
	}
	conv, err := s.GetConversation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.PendingMessageIDs) != 2 || conv.PendingMessageIDs[0] != 2 || conv.PendingMessageIDs[1] != 3 {
		t.Fatalf("rejected steering reordered turns: %v", conv.PendingMessageIDs)
	}
}

func TestFailedRunRequeuesConfirmedAndPendingSteers(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "initial", "web", "owner", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimPendingOwnerMessage(c.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkActiveSubmitted(c.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	confirmed, _, _, err := s.AppendSteeredOwnerMessageWithSource(c.ID, "second", "web", "owner", "", "", "second", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmSteer(c.ID, "run-1", confirmed.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.AppendSteeredOwnerMessageWithSource(c.ID, "third", "web", "owner", "", "", "third", "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.RequeueSteersForRun(c.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	conv, err := s.GetConversation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.PendingMessageIDs) != 2 || conv.PendingMessageIDs[0] != 2 || conv.PendingMessageIDs[1] != 3 || len(conv.SteeredMessageIDs) != 0 {
		t.Fatalf("failed run lost steering inputs: %+v", conv)
	}
	if err := s.ClearActiveRunID(c.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
}

func TestSteerTargetRequiresSubmittedWebTurn(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation("Chat")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.SteerTarget(c.ID); got != "" {
		t.Fatalf("idle conversation steer target: %q", got)
	}
	if _, _, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "busy", "web", "owner", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimPendingOwnerMessage(c.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	if got := s.SteerTarget(c.ID); got != "" {
		t.Fatalf("unsubmitted turn steer target: %q", got)
	}
	if err := s.MarkActiveSubmitted(c.ID, "run-1", 1); err != nil {
		t.Fatal(err)
	}
	if got := s.SteerTarget(c.ID); got != "run-1" {
		t.Fatalf("submitted web turn steer target: %q", got)
	}
	if err := s.ClearActiveRunID(c.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendQueuedOwnerMessageWithSource(c.ID, "matrix turn", "matrix", "owner", "!room:test", "@owner:test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimPendingOwnerMessage(c.ID, "run-2", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkActiveSubmitted(c.ID, "run-2", 2); err != nil {
		t.Fatal(err)
	}
	if got := s.SteerTarget(c.ID); got != "" {
		t.Fatalf("matrix turn steer target: %q", got)
	}
}
