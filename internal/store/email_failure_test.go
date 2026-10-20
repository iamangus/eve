package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmailStateRollsBackCursorAndCascadeOnWriteFailure(t *testing.T) {
	dir := t.TempDir()
	st, err := NewEmailStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	account, err := st.AddAccount(Account{Address: "owner@test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTrigger(Trigger{AccountID: account.ID, Name: "rule"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "email_state.json.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAccountCursor(account.ID, 1, 42); err == nil {
		t.Fatal("expected cursor write failure")
	}
	got, _ := st.GetAccount(account.ID)
	if got.LastUID != 0 || got.UIDValidity != 0 {
		t.Fatal("failed cursor remained in memory")
	}
	if err := st.DeleteAccount(account.ID); err == nil {
		t.Fatal("expected cascade failure")
	}
	if len(st.Accounts()) != 1 || len(st.Triggers()) != 1 {
		t.Fatal("failed cascade changed memory")
	}
	reloaded, err := NewEmailStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Accounts()) != 1 || len(reloaded.Triggers()) != 1 {
		t.Fatal("failed cascade changed disk")
	}
}
