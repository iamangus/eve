package email

import (
	"context"
	"log/slog"
	"time"

	"github.com/iamangus/eve/internal/store"
)

// AccountSource supplies the accounts to poll and persists the fetch cursor.
type AccountSource interface {
	EnabledAccounts() []store.Account
	SetAccountLastUID(id string, uid uint32) error
}

// MessageSink receives each newly fetched message together with its account.
type MessageSink func(ctx context.Context, acct store.Account, msg store.EmailMessage)

// Poller periodically fetches new mail for every enabled account and forwards
// it to the sink. It mirrors the chat Reconcile loop.
type Poller struct {
	accounts AccountSource
	sink     MessageSink
	interval time.Duration
}

func NewPoller(accounts AccountSource, sink MessageSink, interval time.Duration) *Poller {
	return &Poller{accounts: accounts, sink: sink, interval: interval}
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
	for _, acct := range p.accounts.EnabledAccounts() {
		p.pollAccount(ctx, acct)
	}
}

func (p *Poller) pollAccount(ctx context.Context, acct store.Account) {
	msgs, newUID, err := FetchNew(acct)
	if err != nil {
		slog.Warn("email poll", "account", acct.Address, "error", err)
		return
	}
	for _, m := range msgs {
		p.sink(ctx, acct, m)
	}
	if newUID > acct.LastUID {
		if err := p.accounts.SetAccountLastUID(acct.ID, newUID); err != nil {
			slog.Error("email poll cursor", "account", acct.Address, "error", err)
		}
	}
}
