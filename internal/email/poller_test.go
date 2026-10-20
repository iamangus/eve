package email

import (
	"context"
	"errors"
	"github.com/iamangus/eve/internal/store"
	"testing"
	"time"
)

type cursorAccounts struct {
	account store.Account
	writes  int
}

func (a *cursorAccounts) EnabledAccounts() []store.Account { return []store.Account{a.account} }
func (a *cursorAccounts) SetAccountCursor(_ string, validity, uid uint32) error {
	a.writes++
	a.account.UIDValidity = validity
	a.account.LastUID = uid
	return nil
}

func TestPollerRetainsCursorUntilDurableAcceptance(t *testing.T) {
	accounts := &cursorAccounts{account: store.Account{ID: "account", UIDValidity: 1, LastUID: 9}}
	fail := true
	p := NewPoller(accounts, func(context.Context, store.Account, store.EmailMessage) error {
		if fail {
			return errors.New("disk full")
		}
		return nil
	}, time.Second)
	p.fetch = func(context.Context, store.Account, *uint32) ([]store.EmailMessage, uint32, error) {
		return []store.EmailMessage{{UID: 10}}, 10, nil
	}
	p.pollOnce(context.Background())
	if accounts.writes != 0 {
		t.Fatal("cursor advanced after failed acceptance")
	}
	fail = false
	p.pollOnce(context.Background())
	if accounts.account.LastUID != 10 {
		t.Fatal("cursor did not advance after acceptance")
	}
	p.fetch = func(_ context.Context, _ store.Account, validity *uint32) ([]store.EmailMessage, uint32, error) {
		*validity = 2
		return nil, 0, nil
	}
	p.pollOnce(context.Background())
	if accounts.account.UIDValidity != 2 || accounts.account.LastUID != 0 {
		t.Fatal("UIDVALIDITY reset was not persisted")
	}
}
