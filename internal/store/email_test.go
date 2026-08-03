package store

import (
	"testing"
)

func TestEmailStorePersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := NewEmailStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	acct, err := s.AddAccount(Account{
		Address:  "inbox@example.com",
		Host:     "imap.example.com",
		Port:     993,
		Username: "inbox@example.com",
		Password: "secret",
		UseTLS:   true,
		Enabled:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if acct.ID == "" {
		t.Fatal("account ID not assigned")
	}

	trig, err := s.AddTrigger(Trigger{
		AccountID: acct.ID,
		Name:      "invoice",
		Filters:   []Filter{{Field: "sender", Contains: "vendor.com"}},
		Prompt:    "summarize",
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := s.AddRun(TriggerRun{
		TriggerID: trig.ID,
		AccountID: acct.ID,
		Email:     EmailMessage{From: "billing@vendor.com", Subject: "Invoice"},
		Status:    "running",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetAccountLastUID(acct.ID, 42); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewEmailStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(reloaded.Accounts()); got != 1 {
		t.Fatalf("expected 1 account, got %d", got)
	}
	if got := len(reloaded.Triggers()); got != 1 {
		t.Fatalf("expected 1 trigger, got %d", got)
	}
	if got := len(reloaded.Runs()); got != 1 {
		t.Fatalf("expected 1 run, got %d", got)
	}
	a, err := reloaded.GetAccount(acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.LastUID != 42 {
		t.Fatalf("expected LastUID 42, got %d", a.LastUID)
	}
	if a.Password != "secret" {
		t.Fatal("password not persisted")
	}
	got, err := reloaded.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "running" || got.Email.Subject != "Invoice" {
		t.Fatalf("run not persisted correctly: %+v", got)
	}
}

func TestEmailStoreDeleteAccountCascades(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewEmailStore(dir)
	acct, _ := s.AddAccount(Account{Address: "a@b.com"})
	trig, _ := s.AddTrigger(Trigger{AccountID: acct.ID, Name: "t"})
	_, _ = s.AddRun(TriggerRun{TriggerID: trig.ID, AccountID: acct.ID})

	if err := s.DeleteAccount(acct.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.Accounts()) != 0 || len(s.Triggers()) != 0 || len(s.Runs()) != 0 {
		t.Fatal("expected cascading delete of account, triggers, and runs")
	}
}

func TestEmailStoreAtMostOnce(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewEmailStore(dir)

	if s.IsProcessed("acct1", "msg-1") {
		t.Fatal("unprocessed message should not be marked")
	}
	if err := s.MarkProcessed("acct1", "msg-1"); err != nil {
		t.Fatal(err)
	}
	if !s.IsProcessed("acct1", "msg-1") {
		t.Fatal("processed message should be marked")
	}
	if s.IsProcessed("acct1", "msg-2") {
		t.Fatal("different message should not be marked")
	}
	if s.IsProcessed("acct2", "msg-1") {
		t.Fatal("same message id under different account should not be marked")
	}

	reloaded, err := NewEmailStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsProcessed("acct1", "msg-1") {
		t.Fatal("processed set should survive restart")
	}
}

func TestEmailStoreAtMostOnceEmptyMessageID(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewEmailStore(dir)
	if s.IsProcessed("acct1", "") {
		t.Fatal("empty message id should never be treated as processed")
	}
	if err := s.MarkProcessed("acct1", ""); err != nil {
		t.Fatal(err)
	}
	if s.IsProcessed("acct1", "") {
		t.Fatal("empty message id should never be marked processed")
	}
}
