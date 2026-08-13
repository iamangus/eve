package tasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/iamangus/eve/internal/agentfoundry"
)

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("task-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Config configures the background-task manager. All io coupling (router,
// presence, busy check) is injected as functions so tasks never imports io
// (io imports tasks for the MCP tools).
type Config struct {
	PollInterval time.Duration
	// DecisionAgent is the main assistant agent id, used to decide whether a
	// task transition warrants a proactive message.
	DecisionAgent string
	// Cooldown is the minimum gap between decision runs for the same task.
	Cooldown time.Duration
	// Proactive is the master switch for proactive messaging.
	Proactive bool
	// Router delivers a message through the send pipeline.
	Router func(ctx context.Context, convID, content, purpose, channel string) error
	// Presence returns a compact human-readable summary of the user's current
	// presence across channels, for the decision agent.
	Presence func() string
	// IsBusy reports whether a conversation run is in flight (the decision
	// engine must never interrupt an active conversation).
	IsBusy func(convID string) bool
}

type Manager struct {
	store  *Store
	client *agentfoundry.Client
	cfg    Config

	// nextDecide throttles decision runs per task so the engine does not
	// hammer the assistant agent while a message is being held back.
	nextDecide map[string]time.Time
	mu         sync.Mutex
}

func NewManager(st *Store, client *agentfoundry.Client, cfg Config) *Manager {
	return &Manager{
		store:      st,
		client:     client,
		cfg:        cfg,
		nextDecide: make(map[string]time.Time),
	}
}

func (m *Manager) Get(id string) (Task, error)          { return m.store.Get(id) }
func (m *Manager) List() []Task                          { return m.store.List() }
func (m *Manager) ListByConversation(convID string) []Task { return m.store.ListByConversation(convID) }

// SpawnTask kicks off a background subtask: the given agent is run with a
// structured-output schema so it can report completion or request input.
func (m *Manager) SpawnTask(ctx context.Context, convID, agentID, agentName, message string) (Task, error) {
	if strings.TrimSpace(message) == "" {
		return Task{}, fmt.Errorf("task message is required")
	}
	if agentID == "" {
		return Task{}, fmt.Errorf("task agent is required")
	}
	t := Task{
		ID:             newID(),
		ConversationID: convID,
		AgentID:        agentID,
		AgentName:      agentName,
		Message:        message,
		Status:         StatusRunning,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := m.store.Create(&t); err != nil {
		return Task{}, err
	}
	if err := m.startRun(ctx, &t); err != nil {
		_ = m.store.SetFailed(t.ID, "failed to start: "+err.Error())
		return Task{}, err
	}
	slog.Info("task spawned", "task", t.ID, "agent", agentID, "conv", convID)
	return t, nil
}

// Reply appends a user reply and re-runs the child agent with it.
func (m *Manager) Reply(ctx context.Context, id, content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("reply is required")
	}
	existing, err := m.store.Get(id)
	if err != nil {
		return err
	}
	if existing.Terminal() {
		return fmt.Errorf("task %s is %s and cannot receive replies", id, existing.Status)
	}
	if err := m.store.Reply(id, content); err != nil {
		return err
	}
	t, err := m.store.Get(id)
	if err != nil {
		return err
	}
	slog.Info("task replied", "task", id)
	return m.startRun(ctx, &t)
}

// Cancel stops a background task. If the task's child run is still executing
// in agentfoundry it is cancelled first; needs_input tasks have no live run,
// so only the local status is flipped.
func (m *Manager) Cancel(ctx context.Context, id string) error {
	existing, err := m.store.Get(id)
	if err != nil {
		return err
	}
	if existing.Terminal() {
		return fmt.Errorf("task %s is %s and cannot be cancelled", id, existing.Status)
	}
	if existing.RunID != "" && existing.Status == StatusRunning {
		if err := m.client.CancelRun(ctx, existing.RunID); err != nil {
			slog.Warn("cancel task run", "task", id, "run", existing.RunID, "error", err)
		}
	}
	return m.store.Cancel(id)
}

// startRun launches the child agent for a task and records the run id. The
// prompt carries the full task context plus any prior replies. The run is
// tagged with the task id so it can be rediscovered after an eve restart.
func (m *Manager) startRun(ctx context.Context, t *Task) error {
	prompt := m.buildPrompt(t)
	runID, err := m.client.RunAgentWith(ctx, t.AgentID, agentfoundry.RunOptions{
		Message:        prompt,
		ResponseSchema: taskSchema(),
		TaskID:         t.ID,
	})
	if err != nil {
		return fmt.Errorf("task agent run: %w", err)
	}
	return m.store.SetRunID(t.ID, runID)
}

func (m *Manager) buildPrompt(t *Task) string {
	var b strings.Builder
	b.WriteString("You are running a background task for Eve. Complete the task below.\n\n")
	b.WriteString("Task:\n")
	b.WriteString(t.Message)
	b.WriteString("\n")
	if len(t.Replies) > 0 {
		b.WriteString("\nAdditional input from the user so far:\n")
		for i, r := range t.Replies {
			fmt.Fprintf(&b, "%d. %s\n", i+1, r.Content)
		}
	}
	b.WriteString("\nWhen finished, reply with ONLY a JSON object:\n")
	b.WriteString("{\"status\": \"completed\", \"text\": \"<result or short summary>\"}\n")
	b.WriteString("If you need information from the user before you can proceed, reply with ONLY:\n")
	b.WriteString("{\"status\": \"needs_input\", \"text\": \"<the question to ask the user>\"}\n")
	return b.String()
}

// Reconcile re-attaches task runs after an eve restart. Tasks whose run id
// was lost (empty) or no longer resolves in agentfoundry are looked up by
// task id; if the run is found it is re-attached and advanced to its current
// state, otherwise the task is marked failed so it does not hang as running
// forever. Call once before the poller starts.
func (m *Manager) Reconcile(ctx context.Context) {
	for _, t := range m.store.Active() {
		rs, err := m.resolveRun(ctx, t)
		if err != nil {
			slog.Warn("task reconcile", "task", t.ID, "error", err)
			continue
		}
		if rs == nil {
			if t.RunID == "" {
				slog.Warn("task reconcile: no run found", "task", t.ID)
				_ = m.store.SetFailed(t.ID, "task run was lost during restart")
			}
			continue
		}
		switch rs.Status {
		case "completed":
			outcome := parseOutcome(rs.Response)
			if err := m.store.SetOutcome(t.ID, outcome.Status, outcome.Text); err != nil {
				slog.Warn("task reconcile outcome", "task", t.ID, "error", err)
			} else {
				slog.Info("task reconciled", "task", t.ID, "status", outcome.Status)
			}
		case "failed", "error", "cancelled", "canceled":
			_ = m.store.SetFailed(t.ID, "child agent run did not complete")
		case "running", "":
			if t.RunID == "" {
				slog.Info("task run reattached", "task", t.ID)
			}
		}
	}
}

// resolveRun returns the current status for a task's run. When the stored run
// id is missing or stale (unknown/404), it falls back to looking the run up
// by the task id that was tagged on the run request.
func (m *Manager) resolveRun(ctx context.Context, t Task) (*agentfoundry.RunStatus, error) {
	if t.RunID != "" {
		rs, err := m.client.GetRun(ctx, t.RunID)
		if err == nil && rs.Status != "unknown" {
			return rs, nil
		}
		if err != nil {
			slog.Warn("task reconcile get run", "task", t.ID, "run", t.RunID, "error", err)
		}
	}
	return m.client.FindRunByTaskID(ctx, t.ID)
}


func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(m.cfg.PollInterval)
	defer ticker.Stop()
	m.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.poll(ctx)
		}
	}
}

func (m *Manager) poll(ctx context.Context) {
	for _, t := range m.store.Active() {
		if t.RunID == "" {
			continue
		}
		pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		rs, err := m.client.GetRun(pollCtx, t.RunID)
		cancel()
		if err != nil {
			continue // transient; retry on a later tick
		}
		switch rs.Status {
		case "completed":
			outcome := parseOutcome(rs.Response)
			if err := m.store.SetOutcome(t.ID, outcome.Status, outcome.Text); err != nil {
				slog.Warn("task outcome", "task", t.ID, "error", err)
			} else {
				slog.Info("task transition", "task", t.ID, "status", outcome.Status)
			}
		case "failed", "error", "cancelled", "canceled", "unknown":
			if err := m.store.SetFailed(t.ID, "child agent run did not complete"); err != nil {
				slog.Warn("task failed", "task", t.ID, "error", err)
			}
		}
	}
	m.decidePending(ctx)
}

// decidePending surfaces task transitions: for each unreported terminal or
// needs_input task it asks the decision agent whether to message now, then
// delivers through the router. It never interrupts an in-flight conversation.
func (m *Manager) decidePending(ctx context.Context) {
	if m.cfg.DecisionAgent == "" {
		for _, t := range m.store.Unreported() {
			_ = m.store.MarkReported(t.ID)
		}
		return
	}
	for _, t := range m.store.Unreported() {
		m.mu.Lock()
		due := m.nextDecide[t.ID].Before(time.Now())
		m.mu.Unlock()
		if !due {
			continue
		}
		if m.cfg.IsBusy != nil && m.cfg.IsBusy(t.ConversationID) {
			continue // never interrupt an active conversation
		}
		decision := m.decide(ctx, t)
		m.mu.Lock()
		m.nextDecide[t.ID] = time.Now().Add(m.cfg.Cooldown)
		m.mu.Unlock()
		if decision.Action == "wait" || decision.Text == "" {
			continue // hold the message; retry after cooldown
		}
		_ = m.store.MarkReported(t.ID)
		if m.cfg.Router == nil {
			continue
		}
		purpose := "notification"
		if t.Status == StatusNeedsInput {
			purpose = "question"
		}
		if err := m.cfg.Router(ctx, t.ConversationID, decision.Text, purpose, ""); err != nil {
			slog.Warn("task delivery", "task", t.ID, "error", err)
		}
	}
}

// decide asks the decision agent (the main assistant) whether to message the
// user about the task transition. Falls back to WAIT on any failure.
func (m *Manager) decide(ctx context.Context, t Task) decision {
	prompt := m.decisionPrompt(t)
	runID, err := m.client.RunAgentWith(ctx, m.cfg.DecisionAgent, agentfoundry.RunOptions{
		Message:        prompt,
		ResponseSchema: decisionSchema(),
	})
	if err != nil {
		slog.Warn("decision run", "task", t.ID, "error", err)
		return decision{Action: "wait"}
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	text, err := m.client.AwaitRunText(cctx, runID, 25*time.Second)
	if err != nil {
		slog.Warn("decision await", "task", t.ID, "error", err)
		return decision{Action: "wait"}
	}
	d := parseDecision(text)
	return d
}

func (m *Manager) decisionPrompt(t Task) string {
	var b strings.Builder
	b.WriteString("A background task just changed state. Decide whether to message the user about it now or wait for a better moment.\n\n")
	fmt.Fprintf(&b, "Task: %s\n", t.AgentName)
	fmt.Fprintf(&b, "Details: %s\n", t.Message)
	switch t.Status {
	case StatusCompleted:
		fmt.Fprintf(&b, "Outcome: completed\nResult: %s\n", t.Result)
	case StatusNeedsInput:
		fmt.Fprintf(&b, "The task needs input from the user. Question: %s\n", t.Question)
	case StatusFailed:
		fmt.Fprintf(&b, "Outcome: failed\nError: %s\n", t.Result)
	}
	if p := m.cfg.Presence; p != nil {
		fmt.Fprintf(&b, "\nUser presence:\n%s\n", p())
	}
	b.WriteString("\nIf this is worth the user's attention right now, choose message and write the text. If it can wait (e.g. low priority, or the moment is wrong), choose wait.\n")
	b.WriteString("Reply with ONLY a JSON object:\n")
	b.WriteString("{\"action\": \"message\", \"text\": \"<the message to send>\"} or {\"action\": \"wait\"}\n")
	return b.String()
}

// ContextBlock renders the task board injected into the assistant's context
// on every conversation turn.
func (m *Manager) ContextBlock() string {
	tasks := m.store.List()
	var b strings.Builder
	anyActive := false
	b.WriteString("## Background tasks\n")
	for _, t := range tasks {
		if !t.Active() && t.Reported {
			continue
		}
		anyActive = true
		status := t.Status
		if t.Status == StatusNeedsInput {
			fmt.Fprintf(&b, "- [%s] \"%s\" (task %s): %s — waiting on input: %s\n", status, t.AgentName, t.ID, t.Message, t.Question)
			continue
		}
		if t.Status == StatusRunning {
			fmt.Fprintf(&b, "- [%s] \"%s\" (task %s): %s\n", status, t.AgentName, t.ID, t.Message)
			continue
		}
		if !t.Reported {
			fmt.Fprintf(&b, "- [%s] \"%s\" (task %s): %s — %s\n", status, t.AgentName, t.ID, t.Message, t.Result)
		}
	}
	if !anyActive {
		b.WriteString("(none)\n")
	}
	return b.String()
}
