package context

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/iamangus/eve/internal/store"
)

// Status is the snapshot served to the Context tab.
type Status struct {
	Enabled         bool         `json:"enabled"`
	BudgetTokens    int          `json:"budget_tokens"`
	RenderedTokens  int          `json:"rendered_tokens"`
	Pressure        float64      `json:"pressure"`
	Sources         SourceTokens `json:"sources"`
	Historian       HistStatus   `json:"historian"`
	Coverage        Coverage     `json:"coverage"`
	Compartments    []CompView   `json:"compartments"`
	Memories        []store.Memory `json:"memories"`
}

type SourceTokens struct {
	Compartments int `json:"compartments"`
	Memories     int `json:"memories"`
	RawTail      int `json:"raw_tail"`
}

type HistStatus struct {
	Running            bool      `json:"running"`
	LastRunAt          time.Time `json:"last_run_at"`
	LastError          string    `json:"last_error,omitempty"`
	BoundaryMsgID      int64     `json:"boundary_msg_id"`
	UnsummarizedTokens int       `json:"unsummarized_tokens"`
	TriggerThreshold   int       `json:"trigger_threshold"`
}

type Coverage struct {
	TotalMessages    int `json:"total_messages"`
	Compartmentalized int `json:"compartmentalized"`
	Raw              int `json:"raw"`
}

type CompView struct {
	ID         string      `json:"id"`
	StartMsgID int64       `json:"start_msg_id"`
	EndMsgID   int64       `json:"end_msg_id"`
	CreatedAt  time.Time   `json:"created_at"`
	Importance int         `json:"importance"`
	Tier       string      `json:"tier"`
	Summary    string      `json:"summary"`
	Facts      []store.Fact `json:"facts"`
}

// Snapshot builds the current context state for the primary conversation.
func (m *Manager) Snapshot() Status {
	mems := m.store.Memories()
	if mems == nil {
		mems = []store.Memory{}
	}
	status := Status{
		Enabled:      m.Enabled(),
		BudgetTokens: m.cfg.BudgetTokens,
		Memories:     mems,
		Compartments: []CompView{},
	}
	convID := m.store.PrimaryConversationID()
	if convID == "" {
		return status
	}

	msgs, err := m.store.ConversationHistory(convID)
	if err != nil {
		return status
	}
	boundary, err := m.store.SummarizedUpTo(convID)
	if err != nil {
		return status
	}
	status.Historian.BoundaryMsgID = boundary
	status.Coverage.TotalMessages = len(msgs)
	for _, msg := range msgs {
		if msg.ID <= boundary {
			status.Coverage.Compartmentalized++
		} else {
			status.Coverage.Raw++
			status.Historian.UnsummarizedTokens += EstimateTokens(msg.Content)
		}
	}
	status.Historian.TriggerThreshold = int(float64(m.cfg.BudgetTokens) * m.cfg.TriggerFraction)
	status.Historian.Running = m.running.Load()
	if runAt, ok := m.lastRunAt.Load().(time.Time); ok {
		status.Historian.LastRunAt = runAt
	}
	if errMsg, ok := m.lastError.Load().(string); ok {
		status.Historian.LastError = errMsg
	}

	// Reuse the deterministic render to report tiers and sizes.
	_, stats, _ := m.RenderHistory(convID)
	status.RenderedTokens = stats.RenderedTokens
	status.Pressure = stats.Pressure
	status.Sources = SourceTokens{
		Compartments: stats.CompartmentTokens,
		Memories:     stats.MemoryTokens,
		RawTail:      stats.TailTokens,
	}

	tierByStart := make(map[int64]string)
	for _, rc := range renderedCompartmentsForReport(m.store.Compartments(convID), budgetTarget(m.cfg, stats.TailTokens, stats.MemoryTokens)) {
		tierByStart[rc.comp.StartMsgID] = tierName(rc.tier)
	}
	for _, comp := range m.store.Compartments(convID) {
		v := CompView{
			ID:         comp.ID,
			StartMsgID: comp.StartMsgID,
			EndMsgID:   comp.EndMsgID,
			CreatedAt:  comp.CreatedAt,
			Importance: comp.Importance,
			Tier:       "dropped",
			Summary:    comp.Tiers.P1,
			Facts:      comp.Facts,
		}
		if t, ok := tierByStart[comp.StartMsgID]; ok {
			v.Tier = t
			switch t {
			case "p1":
				v.Summary = comp.Tiers.P1
			case "p2":
				v.Summary = comp.Tiers.P2
			case "p3":
				v.Summary = comp.Tiers.P3
			case "p4":
				v.Summary = comp.Tiers.P4
			}
		}
		status.Compartments = append(status.Compartments, v)
	}
	return status
}

// renderedCompartmentsForReport re-runs the deterministic render for reporting.
func renderedCompartmentsForReport(comps []store.Compartment, target int) []renderedCompartment {
	return renderCompartments(comps, target)
}

func budgetTarget(cfg Config, tailTokens, memTokens int) int {
	target := cfg.BudgetTokens - tailTokens - memTokens - int(float64(cfg.BudgetTokens)*reserveRatio)
	if target < 0 {
		return 0
	}
	return target
}

func tierName(tier int) string {
	switch tier {
	case 1:
		return "p1"
	case 2:
		return "p2"
	case 3:
		return "p3"
	case 4:
		return "p4"
	default:
		return "dropped"
	}
}

func (m *Manager) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/context", m.getContext)
	mux.HandleFunc("POST /api/context/compact", m.postCompact)
}

func (m *Manager) getContext(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, m.Snapshot())
}

func (m *Manager) postCompact(w http.ResponseWriter, r *http.Request) {
	if err := m.ForceCompact(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode json", "error", err)
	}
}
