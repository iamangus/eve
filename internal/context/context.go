package context

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/store"
)

// Config controls the historian and decay rendering.
type Config struct {
	// AgentID is the agentfoundry agent that produces compartment manifests.
	// Empty disables context management (history is sent raw).
	AgentID string
	// BudgetTokens is the target size of the rendered history in tokens.
	BudgetTokens int
	// TriggerFraction of the budget at which the historian fires.
	TriggerFraction float64
	// ProtectedTailTokens of the newest raw history always stay raw.
	ProtectedTailTokens int
	// ChunkTokens caps the size of one historian run's input.
	ChunkTokens int
	// MemoryLimit caps the memory pool (curation evicts beyond it).
	MemoryLimit int
	// CurateInterval schedules memory curation.
	CurateInterval time.Duration
}

var errNothingToDo = errors.New("no compactible messages")

// Manager owns the historian loop and context rendering.
type Manager struct {
	store  *store.Store
	client *agentfoundry.Client
	cfg    Config

	running   atomic.Bool
	lastRunAt atomic.Value // time.Time
	lastError atomic.Value // string
	wake      chan struct{}
}

func NewManager(st *store.Store, client *agentfoundry.Client, cfg Config) *Manager {
	m := &Manager{store: st, client: client, cfg: cfg, wake: make(chan struct{}, 1)}
	m.lastRunAt.Store(time.Time{})
	m.lastError.Store("")
	return m
}

// Enabled reports whether a historian agent is configured.
func (m *Manager) Enabled() bool { return m.cfg.AgentID != "" }

// Loop runs periodic maintenance: a trigger check every 30s (also covers
// startup catch-up of pre-existing history), memory curation on its own
// interval, and wakes on force-compact requests.
func (m *Manager) Loop(ctx context.Context) {
	if !m.Enabled() {
		return
	}
	triggerTicker := time.NewTicker(30 * time.Second)
	defer triggerTicker.Stop()
	curateTicker := time.NewTicker(m.curateInterval())
	defer curateTicker.Stop()

	m.MaybeCompact(ctx)
	m.curate()
	for {
		select {
		case <-ctx.Done():
			return
		case <-triggerTicker.C:
			m.MaybeCompact(ctx)
		case <-curateTicker.C:
			m.curate()
		case <-m.wake:
			m.MaybeCompact(ctx)
		}
	}
}

func (m *Manager) curateInterval() time.Duration {
	if m.cfg.CurateInterval > 0 {
		return m.cfg.CurateInterval
	}
	return 24 * time.Hour
}

// MaybeCompact fires a historian run when the unsummarized tail exceeds the
// trigger threshold. Non-blocking; runs in a background goroutine.
func (m *Manager) MaybeCompact(ctx context.Context) {
	if !m.Enabled() || m.running.Load() {
		return
	}
	convID := m.store.PrimaryConversationID()
	if convID == "" {
		return
	}
	tailTokens, err := m.unsummarizedTokens(convID)
	if err != nil {
		return
	}
	if tailTokens < int(float64(m.cfg.BudgetTokens)*m.cfg.TriggerFraction) {
		return
	}
	go m.runOnce(ctx, convID)
}

// ForceCompact triggers a historian run regardless of size. Non-blocking.
func (m *Manager) ForceCompact(ctx context.Context) error {
	if !m.Enabled() {
		return fmt.Errorf("no historian agent configured")
	}
	if m.running.Load() {
		return nil
	}
	convID := m.store.PrimaryConversationID()
	if convID == "" {
		return fmt.Errorf("no conversation yet")
	}
	go m.runOnce(ctx, convID)
	return nil
}

// unsummarizedTokens estimates the size of the raw tail after the last
// compartment boundary.
func (m *Manager) unsummarizedTokens(convID string) (int, error) {
	msgs, err := m.store.ConversationHistory(convID)
	if err != nil {
		return 0, err
	}
	boundary, err := m.store.SummarizedUpTo(convID)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, msg := range msgs {
		if msg.ID > boundary {
			total += EstimateTokens(msg.Content)
		}
	}
	return total, nil
}

// runOnce performs a single historian pass. Guarded by m.running.
func (m *Manager) runOnce(ctx context.Context, convID string) {
	if !m.running.CompareAndSwap(false, true) {
		return
	}
	defer m.running.Store(false)

	slog.Info("historian run starting", "conv", convID)
	runAt := time.Now()
	err := m.compact(ctx, convID)
	m.lastRunAt.Store(runAt)
	if errors.Is(err, errNothingToDo) {
		return
	}
	if err != nil {
		slog.Error("historian run failed", "conv", convID, "error", err)
		m.lastError.Store(err.Error())
		_ = m.store.SetHistorianState(convID, runAt, err.Error())
		return
	}
	slog.Info("historian run complete", "conv", convID)
	m.lastError.Store("")
	_ = m.store.SetHistorianState(convID, runAt, "")
}

// compact summarizes the oldest eligible chunk of the unsummarized tail and
// persists the resulting compartment plus promoted facts.
func (m *Manager) compact(ctx context.Context, convID string) error {
	msgs, err := m.store.ConversationHistory(convID)
	if err != nil {
		return err
	}
	boundary, err := m.store.SummarizedUpTo(convID)
	if err != nil {
		return err
	}
	var tail []store.Message
	for _, msg := range msgs {
		if msg.ID > boundary {
			tail = append(tail, msg)
		}
	}
	chunk := m.selectChunk(tail)
	if len(chunk) == 0 {
		return errNothingToDo
	}

	runCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()

	runID, err := m.client.RunAgent(runCtx, m.cfg.AgentID, buildChunkText(chunk), nil)
	if err != nil {
		return fmt.Errorf("historian run: %w", err)
	}
	text, err := m.client.AwaitRunText(runCtx, runID, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("historian await: %w", err)
	}
	manifest, err := ParseManifest(text)
	if err != nil {
		return fmt.Errorf("historian manifest: %w", err)
	}

	first, last := chunk[0], chunk[len(chunk)-1]
	best := manifest.Compartments[0]
	comp := store.Compartment{
		StartMsgID: first.ID,
		EndMsgID:   last.ID,
		CreatedAt:  time.Now(),
		Importance: clampImportance(best.Importance),
		Tiers: store.CompartmentTiers{
			P1: strings.TrimSpace(best.P1),
			P2: strings.TrimSpace(best.P2),
			P3: strings.TrimSpace(best.P3),
			P4: strings.TrimSpace(best.P4),
		},
	}
	for _, c := range manifest.Compartments {
		for _, f := range c.Facts {
			content := strings.TrimSpace(f.Content)
			if content == "" {
				continue
			}
			comp.Facts = append(comp.Facts, store.Fact{
				Category: CanonicalCategory(f.Category),
				Content:  content,
			})
		}
	}
	stored, err := m.store.AddCompartment(convID, comp)
	if err != nil {
		return err
	}
	if err := m.promoteFacts(convID, stored); err != nil {
		slog.Warn("promote facts", "conv", convID, "error", err)
	}
	return nil
}

// selectChunk picks the oldest messages to summarize: at most ChunkTokens
// worth, never touching the newest ProtectedTailTokens of raw history.
func (m *Manager) selectChunk(tail []store.Message) []store.Message {
	if len(tail) == 0 {
		return nil
	}
	// Walk from the end leaving the protected tail raw.
	end := len(tail)
	acc := 0
	for i := len(tail) - 1; i >= 0; i-- {
		acc += EstimateTokens(tail[i].Content)
		if acc >= m.cfg.ProtectedTailTokens {
			end = i
			break
		}
	}
	if end <= 0 {
		return nil
	}
	// Walk from the start capping the chunk size.
	cut := end
	acc = 0
	for i := 0; i < end; i++ {
		acc += EstimateTokens(tail[i].Content)
		if acc >= m.cfg.ChunkTokens {
			cut = i + 1
			break
		}
	}
	return append([]store.Message(nil), tail[:cut]...)
}

func (m *Manager) promoteFacts(convID string, comp store.Compartment) error {
	for _, f := range comp.Facts {
		hash := factHash(f.Category, f.Content)
		if m.store.MemoryByHash(hash) {
			continue
		}
		if err := m.store.AddMemory(store.Memory{
			Category:          f.Category,
			Content:           f.Content,
			Importance:        comp.Importance,
			SourceCompartment: comp.ID,
			Hash:              hash,
		}); err != nil {
			return err
		}
	}
	return nil
}

// buildChunkText formats a chunk with role labels and relative timestamps so
// the historian can preserve temporal structure.
func buildChunkText(chunk []store.Message) string {
	var b strings.Builder
	b.WriteString("Below is a chunk of a longer conversation. Compress it per your instructions.\n\n")
	for _, msg := range chunk {
		b.WriteString("[")
		b.WriteString(timeAgo(msg.CreatedAt))
		b.WriteString("] ")
		b.WriteString(msg.Role)
		b.WriteString(": ")
		b.WriteString(msg.Content)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh %dm ago", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func clampImportance(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func factHash(category, content string) string {
	sum := sha256.Sum256([]byte(category + "|" + content))
	return hex.EncodeToString(sum[:8])
}

// manifestCompartment is the JSON shape emitted by the historian agent.
type manifestCompartment struct {
	Importance int `json:"importance"`
	P1         string `json:"p1"`
	P2         string `json:"p2"`
	P3         string `json:"p3"`
	P4         string `json:"p4"`
	Facts      []struct {
		Category string `json:"category"`
		Content  string `json:"content"`
	} `json:"facts"`
}

// Manifest is the parsed historian output.
type Manifest struct {
	Compartments []manifestCompartment
}

// ParseManifest parses the historian's JSON output, leniently: code fences
// and surrounding prose are tolerated. Fail-closed: an output with no
// usable compartments is an error.
func ParseManifest(text string) (Manifest, error) {
	var envelope struct {
		Compartments []manifestCompartment `json:"compartments"`
	}
	raw := []byte(text)
	if err := json.Unmarshal(raw, &envelope); err != nil {
		if err := json.Unmarshal([]byte(stripFences(text)), &envelope); err != nil {
			return Manifest{}, fmt.Errorf("invalid JSON: %w", err)
		}
	}
	usable := make([]manifestCompartment, 0, len(envelope.Compartments))
	for _, c := range envelope.Compartments {
		if strings.TrimSpace(c.P1) == "" {
			continue
		}
		usable = append(usable, c)
	}
	if len(usable) == 0 {
		return Manifest{}, fmt.Errorf("no usable compartments in output")
	}
	return Manifest{Compartments: usable}, nil
}

// stripFences removes ```json ... ``` fences from the output, tolerating
// leading and trailing prose.
func stripFences(text string) string {
	s := strings.TrimSpace(text)
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if j := strings.Index(rest, "\n"); j >= 0 {
			rest = rest[j+1:]
		}
		if k := strings.LastIndex(rest, "```"); k >= 0 {
			rest = rest[:k]
		}
		s = strings.TrimSpace(rest)
	}
	return s
}
