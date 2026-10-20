package email

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/iamangus/eve/internal/store"
)

// AccountSource supplies the accounts to poll and persists the fetch cursor.
type AccountSource interface {
	EnabledAccounts() []store.Account
	SetAccountCursor(id string, validity, uid uint32) error
}

// MessageSink receives each newly fetched message together with its account.
type MessageSink func(ctx context.Context, acct store.Account, msg store.EmailMessage) error

// Poller periodically fetches new mail for every enabled account and forwards
// it to the sink. It mirrors the chat Reconcile loop.
type Poller struct {
	accounts AccountSource
	sink     MessageSink
	interval time.Duration
	onHealth func(error)
	fetch    func(context.Context, store.Account, *uint32) ([]store.EmailMessage, uint32, error)
}

func NewPoller(accounts AccountSource, sink MessageSink, interval time.Duration) *Poller {
	return &Poller{accounts: accounts, sink: sink, interval: interval, fetch: fetchNew}
}

// SetHealthHook registers a callback invoked after each account poll with its
// outcome, so callers can surface poller health (last check / last error).
func (p *Poller) SetHealthHook(fn func(error)) {
	p.onHealth = fn
}

func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	p.pollOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollOnce(ctx)
		}
	}
}

func (p *Poller) pollOnce(ctx context.Context) {
	var failure error
	for _, acct := range p.accounts.EnabledAccounts() {
		failure = errors.Join(failure, p.pollAccount(ctx, acct))
	}
	if p.onHealth != nil {
		p.onHealth(failure)
	}
}

func (p *Poller) pollAccount(ctx context.Context, acct store.Account) error {
	validity := acct.UIDValidity
	previousValidity := validity
	msgs, newUID, err := p.fetch(ctx, acct, &validity)
	if err != nil {
		slog.Warn("email poll", "account", acct.Address, "error", err)
		return err
	}
	for _, m := range msgs {
		acct.UIDValidity = validity
		if err := p.sink(ctx, acct, m); err != nil {
			slog.Warn("email acceptance failed; cursor retained", "account", acct.ID, "uid", m.UID, "error", err)
			return err
		}
	}
	if newUID > acct.LastUID || validity != previousValidity {
		if err := p.accounts.SetAccountCursor(acct.ID, validity, newUID); err != nil {
			slog.Error("email poll cursor", "account", acct.Address, "error", err)
			return err
		}
	}
	return nil
}
