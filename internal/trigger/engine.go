package trigger

import (
	"context"
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
// trigger of the account and fires the agent for each match.
func (e *Engine) HandleEmail(ctx context.Context, acct store.Account, msg store.EmailMessage) {
	triggers, err := e.store.EnabledTriggersForAccount(acct.ID)
	if err != nil {
		slog.Error("load triggers", "account", acct.ID, "error", err)
		return
	}
	for _, t := range triggers {
		if Matches(t, msg) {
			e.fire(ctx, t, msg)
		}
	}
}

func (e *Engine) fire(ctx context.Context, t store.Trigger, msg store.EmailMessage) {
	prompt := ComposePrompt(t, msg)
	run := store.TriggerRun{
		TriggerID: t.ID,
		AccountID: t.AccountID,
		Email:     msg,
		Prompt:    prompt,
		Status:    "running",
		CreatedAt: time.Now(),
	}

	runID, err := e.client.RunAgent(ctx, e.agentID, prompt, nil)
	if err != nil {
		run.Status = "failed"
		run.Error = err.Error()
		if _, serr := e.store.AddRun(run); serr != nil {
			slog.Error("store run", "trigger", t.ID, "error", serr)
		}
		slog.Warn("trigger agent run", "trigger", t.ID, "error", err)
		return
	}
	run.AgentRunID = runID
	stored, err := e.store.AddRun(run)
	if err != nil {
		slog.Error("store run", "trigger", t.ID, "error", err)
		return
	}
	go e.finishRun(stored.ID, t.ID, runID)
}

func (e *Engine) finishRun(runID, triggerID, agentRunID string) {
	text, err := e.client.AwaitRunText(context.Background(), agentRunID, e.runTimeout)
	run, gerr := e.store.GetRun(runID)
	if gerr != nil {
		slog.Error("get run", "run", runID, "error", gerr)
		return
	}
	if err != nil {
		run.Status = "failed"
		run.Error = err.Error()
		slog.Warn("trigger await", "trigger", triggerID, "run", agentRunID, "error", err)
	} else {
		run.Status = "completed"
		run.Result = text
	}
	if err := e.store.UpdateRun(run); err != nil {
		slog.Error("update run", "run", runID, "error", err)
	}
}
