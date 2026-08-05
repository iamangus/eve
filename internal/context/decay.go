package context

import (
	"sort"
	"unicode/utf8"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/store"
)

// EstimateTokens is a deterministic token estimate used for budget math.
// Roughly 4 characters per token for mixed prose.
func EstimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	return (n + 3) / 4
}

const (
	// maxCompartments caps how many compartments are ever rendered; the
	// oldest beyond this count are dropped (their raw messages stay on disk).
	maxCompartments = 40
	// memoryBlockLimit is how many memories are always injected up front.
	memoryBlockLimit = 8
	// emergencyRatio is the hard cap on rendered history relative to budget.
	emergencyRatio = 1.1
	// reserveRatio keeps headroom for the outgoing user message and response.
	reserveRatio = 0.1
)

type RenderStats struct {
	RenderedTokens    int     `json:"rendered_tokens"`
	BudgetTokens      int     `json:"budget_tokens"`
	Pressure          float64 `json:"pressure"`
	CompartmentTokens int     `json:"compartment_tokens"`
	MemoryTokens      int     `json:"memory_tokens"`
	TailTokens        int     `json:"tail_tokens"`
	CompartmentsUsed  int     `json:"compartments_used"`
	CompartmentsTotal int     `json:"compartments_total"`
	Truncated         bool    `json:"truncated"`
}

type renderedCompartment struct {
	comp   store.Compartment
	tier   int
	tokens int
}

// RenderHistory builds the history sent to the agent: a memory block, the
// decay-rendered compartments, then the raw unsummarized tail. Rendering is
// deterministic — no LLM in this path.
func (m *Manager) RenderHistory(convID string) ([]agentfoundry.Message, RenderStats, error) {
	msgs, err := m.store.ConversationHistory(convID)
	if err != nil {
		return nil, RenderStats{}, err
	}
	boundary, err := m.store.SummarizedUpTo(convID)
	if err != nil {
		return nil, RenderStats{}, err
	}

	var tail []store.Message
	for _, msg := range msgs {
		if msg.ID > boundary {
			tail = append(tail, msg)
		}
	}
	tailTokens := 0
	for _, msg := range tail {
		tailTokens += EstimateTokens(msg.Content)
	}

	mems := m.store.Memories()
	memText := memoryBlock(mems, memoryBlockLimit)
	memTokens := EstimateTokens(memText)

	budget := m.cfg.BudgetTokens
	reserve := int(float64(budget) * reserveRatio)
	compTarget := budget - tailTokens - memTokens - reserve
	if compTarget < 0 {
		compTarget = 0
	}

	comps := m.store.Compartments(convID)
	rendered := renderCompartments(comps, compTarget)

	history := make([]agentfoundry.Message, 0, 1+len(rendered)+len(tail))
	compTokens := 0
	if memText != "" {
		history = append(history, agentfoundry.Message{Role: "user", Content: memText})
	}
	for _, rc := range rendered {
		text := compartmentText(rc)
		if text == "" {
			continue
		}
		compTokens += rc.tokens
		history = append(history, agentfoundry.Message{Role: "user", Content: text})
	}
	for _, msg := range tail {
		history = append(history, agentfoundry.Message{Role: msg.Role, Content: msg.Content})
	}

	stats := RenderStats{
		BudgetTokens:      budget,
		CompartmentTokens: compTokens,
		MemoryTokens:      memTokens,
		TailTokens:        tailTokens,
		RenderedTokens:    compTokens + memTokens + tailTokens,
		CompartmentsTotal: len(comps),
	}
	if len(rendered) > 0 {
		stats.CompartmentsUsed = len(rendered)
	}
	if budget > 0 {
		stats.Pressure = float64(stats.RenderedTokens) / float64(budget)
	}

	// Emergency: still over the hard cap — drop oldest raw messages above the
	// protected tail, with an explicit marker. Never silent.
	if stats.RenderedTokens > int(float64(budget)*emergencyRatio) && len(tail) > 0 {
		keep := m.protectedTail(tail)
		history = history[:len(history)-len(tail)]
		history = append(history, agentfoundry.Message{
			Role:    "user",
			Content: "[Earlier history truncated to stay within the context budget.]",
		})
		for _, msg := range keep {
			history = append(history, agentfoundry.Message{Role: msg.Role, Content: msg.Content})
		}
		stats.Truncated = true
	}
	return history, stats, nil
}

// protectedTail keeps the newest PROTECTED_TAIL_TOKENS worth of raw messages,
// always including the newest user turn.
func (m *Manager) protectedTail(tail []store.Message) []store.Message {
	acc := 0
	lastUser := len(tail) - 1
	for i := len(tail) - 1; i >= 0; i-- {
		if tail[i].Role == "user" {
			lastUser = i
			break
		}
	}
	start := 0
	for i := len(tail) - 1; i >= 0; i-- {
		acc += EstimateTokens(tail[i].Content)
		if acc >= m.cfg.ProtectedTailTokens {
			start = i
			break
		}
	}
	if start > lastUser {
		start = lastUser
	}
	return append([]store.Message(nil), tail[start:]...)
}

// renderCompartments assigns each compartment the richest tier that fits the
// token target, demoting oldest-first (tie-break: lower importance). Tier
// 5 means dropped. Deterministic.
func renderCompartments(comps []store.Compartment, target int) []renderedCompartment {
	if len(comps) == 0 || target <= 0 {
		return nil
	}
	// Cap the number of compartments ever rendered.
	if len(comps) > maxCompartments {
		comps = comps[len(comps)-maxCompartments:]
	}

	out := make([]renderedCompartment, 0, len(comps))
	for _, c := range comps {
		out = append(out, renderedCompartment{comp: c, tier: 1, tokens: EstimateTokens(c.Tiers.P1)})
	}

	total := 0
	for i := range out {
		total += out[i].tokens
	}

	order := make([]int, len(out))
	for i := range order {
		order[i] = i
	}
	// Oldest-first; tie-break by importance ascending.
	sort.SliceStable(order, func(a, b int) bool {
		ca, cb := out[order[a]].comp, out[order[b]].comp
		if ca.CreatedAt.Equal(cb.CreatedAt) {
			return ca.Importance < cb.Importance
		}
		return ca.CreatedAt.Before(cb.CreatedAt)
	})

	// Demote oldest-first: take the oldest still-eligible compartment and push
	// it down through its tiers until the target fits (or it is dropped),
	// before moving to the next-oldest.
	for total > target {
		demoted := false
		for _, i := range order {
			for out[i].tier < 5 && total > target {
				before := out[i].tokens
				out[i].tier++
				out[i].tokens = tierTokens(out[i])
				total += out[i].tokens - before
				demoted = true
			}
			if total <= target {
				break
			}
		}
		if !demoted {
			break
		}
	}

	kept := make([]renderedCompartment, 0, len(out))
	for _, rc := range out {
		if rc.tier >= 5 {
			continue
		}
		kept = append(kept, rc)
	}
	return kept
}

func tierTokens(rc renderedCompartment) int {
	switch rc.tier {
	case 2:
		return EstimateTokens(rc.comp.Tiers.P2)
	case 3:
		return EstimateTokens(rc.comp.Tiers.P3)
	case 4:
		return EstimateTokens(rc.comp.Tiers.P4)
	default:
		return 0
	}
}

func compartmentText(rc renderedCompartment) string {
	var text string
	switch rc.tier {
	case 1:
		text = rc.comp.Tiers.P1
	case 2:
		text = rc.comp.Tiers.P2
	case 3:
		text = rc.comp.Tiers.P3
	case 4:
		text = rc.comp.Tiers.P4
	default:
		return ""
	}
	if text == "" {
		return ""
	}
	return "[Earlier conversation — summary]\n" + text
}
