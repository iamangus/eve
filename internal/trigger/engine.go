package trigger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/store"
)

// Engine evaluates incoming email against the enabled triggers of its account
// and runs the assistant agent for every match.
type Engine struct {
	store      *store.EmailStore
	client     *agentfoundry.Client
	agentID    string
	runTimeout time.Duration
}

func NewEngine(st *store.EmailStore, client *agentfoundry.Client, agentID string, runTimeout time.Duration) *Engine {
	return &Engine{store: st, client: client, agentID: agentID, runTimeout: runTimeout}
}

// HandleEmail is the poller sink: it matches the message against every enabled
// trigger of the account and fires the agent for each match. Messages already
// handled (by Message-ID) are skipped so a delivery never re-triggers.
func (e *Engine) HandleEmail(ctx context.Context, acct store.Account, msg store.EmailMessage) error {
	if e.store.IsProcessed(acct.ID, msg.MessageID) {
		return nil
	}
	triggers, err := e.store.EnabledTriggersForAccount(acct.ID)
	if err != nil {
		slog.Error("load triggers", "account", acct.ID, "error", err)
		return err
	}
	matched := false
	for _, t := range triggers {
		if Matches(t, msg) {
			matched = true
			if err := e.fire(ctx, t, acct, msg); err != nil {
				return err
			}
		}
	}
	if matched {
		if err := e.store.MarkProcessed(acct.ID, msg.MessageID); err != nil {
			slog.Error("mark processed", "account", acct.ID, "error", err)
			return err
		}
	}
	return nil
}

func (e *Engine) fire(ctx context.Context, t store.Trigger, acct store.Account, msg store.EmailMessage) error {
	prompt := ComposePrompt(t, msg)
	key := fmt.Sprintf("%s:%s:%d:%d:%s", t.ID, acct.ID, acct.UIDValidity, msg.UID, msg.MessageID)
	sum := sha256.Sum256([]byte(key))
	id := "trigger-" + hex.EncodeToString(sum[:16])
	if _, err := e.store.GetRun(id); err == nil {
		return nil
	}
	run := store.TriggerRun{
		ID:        id,
		TriggerID: t.ID,
		AccountID: t.AccountID,
		Email:     msg,
		Prompt:    prompt,
		Status:    "pending",
		CreatedAt: time.Now(),
	}

	_, err := e.store.AddRun(run)
	return err
}

func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		e.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (e *Engine) poll(ctx context.Context) {
	for _, run := range e.store.Runs() {
		before := run
		if ctx.Err() != nil {
			return
		}
		if run.Status != "pending" && run.Status != "running" {
			continue
		}
		if time.Now().Before(run.NextAttemptAt) {
			continue
		}
		pollCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if run.AgentRunID == "" {
			id, err := e.client.RunAgentWith(pollCtx, e.agentID, agentfoundry.RunOptions{Message: run.Prompt, TaskID: run.ID})
			if err != nil {
				run.Attempts++
				run.Error = err.Error()
				run.NextAttemptAt = time.Now().Add(time.Second << min(run.Attempts, 8))
			} else {
				run.AgentRunID = id
				run.StartedAt = time.Now().UTC()
				run.Status = "running"
				run.Error = ""
			}
		} else {
			started := run.StartedAt
			if started.IsZero() {
				started = run.CreatedAt
			}
			if e.runTimeout > 0 && time.Since(started) > e.runTimeout {
				if err := e.client.CancelRun(pollCtx, run.AgentRunID); err == nil || agentfoundry.IsStaleRun(err) {
					run.Status = "failed"
					run.Error = "email trigger run exceeded its deadline"
				}
				cancel()
				if run != before {
					if err := e.store.UpdateRun(run); err != nil {
						slog.Warn("persist expired trigger", "error", err)
					}
				}
				continue
			}
			status, err := e.client.GetRun(pollCtx, run.AgentRunID)
			if err == nil {
				switch status.Status {
				case "completed":
					run.Status = "completed"
					run.Result = status.Response
				case "failed", "error", "cancelled", "canceled", "unknown":
					run.Status = "failed"
					run.Error = status.Error
				}
			}
		}
		cancel()
		if run != before {
			if err := e.store.UpdateRun(run); err != nil {
				slog.Warn("persist trigger progress", "run", run.ID, "error", err)
			}
		}
	}
}
