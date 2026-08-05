package context

import (
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/iamangus/eve/internal/store"
)

var validCategories = map[string]bool{
	"USER_PREFERENCES": true,
	"DECISIONS":        true,
	"CONSTRAINTS":      true,
	"FACTS":            true,
	"NAMING":           true,
}

// CanonicalCategory maps an arbitrary category string to the known taxonomy,
// defaulting unknown values to FACTS.
func CanonicalCategory(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	if validCategories[c] {
		return c
	}
	return "FACTS"
}

// memoryBlock renders the top memories by importance as a leading user-role
// context block. Returns "" when there is nothing to inject.
func memoryBlock(mems []store.Memory, limit int) string {
	if len(mems) == 0 || limit <= 0 {
		return ""
	}
	sorted := append([]store.Memory(nil), mems...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Importance == sorted[j].Importance {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		}
		return sorted[i].Importance > sorted[j].Importance
	})
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	var b strings.Builder
	b.WriteString("[What I remember about you and our past conversations]\n")
	for _, mem := range sorted {
		b.WriteString("- ")
		b.WriteString(mem.Content)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// RecallMemories scores memories against a query by keyword overlap, boosting
// importance. Deterministic; no embeddings.
func RecallMemories(mems []store.Memory, query string, limit int) []store.Memory {
	words := wordSet(query)
	if len(words) == 0 {
		return nil
	}
	type scored struct {
		mem   store.Memory
		score float64
	}
	var scoredMems []scored
	for _, mem := range mems {
		mw := wordSet(mem.Content)
		hits := 0
		for w := range words {
			if mw[w] {
				hits++
			}
		}
		if hits == 0 {
			continue
		}
		s := float64(hits)/float64(len(words)) + float64(mem.Importance)/200.0
		scoredMems = append(scoredMems, scored{mem: mem, score: s})
	}
	sort.Slice(scoredMems, func(i, j int) bool { return scoredMems[i].score > scoredMems[j].score })
	if len(scoredMems) > limit {
		scoredMems = scoredMems[:limit]
	}
	out := make([]store.Memory, 0, len(scoredMems))
	for _, s := range scoredMems {
		out = append(out, s.mem)
	}
	return out
}

// wordSet tokenizes text into lowercase words of length >= 3.
func wordSet(s string) map[string]bool {
	out := make(map[string]bool)
	var word strings.Builder
	flush := func() {
		if word.Len() >= 3 {
			out[word.String()] = true
		}
		word.Reset()
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// curate consolidates the memory pool: merge near-duplicates (content
// substring of another, or high word overlap), drop stale low-importance
// entries, and cap the pool at the configured limit.
func (m *Manager) curate() {
	mems := m.store.Memories()
	if len(mems) == 0 {
		return
	}

	var kept []store.Memory
	for _, mem := range mems {
		dup := false
		for _, other := range kept {
			if mem.Hash != "" && other.Hash != "" && mem.Hash == other.Hash {
				dup = true
				break
			}
			if isNearDuplicate(mem.Content, other.Content) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		if mem.Importance < 20 && time.Since(mem.CreatedAt) > 90*24*time.Hour {
			continue
		}
		kept = append(kept, mem)
	}

	if m.cfg.MemoryLimit > 0 && len(kept) > m.cfg.MemoryLimit {
		sort.Slice(kept, func(i, j int) bool {
			ti := kept[i].Importance + int(kept[i].CreatedAt.Unix()/86400/30)
			tj := kept[j].Importance + int(kept[j].CreatedAt.Unix()/86400/30)
			return ti > tj
		})
		kept = kept[:m.cfg.MemoryLimit]
	}

	if len(kept) != len(mems) {
		_ = m.store.ReplaceMemories(kept)
	}
}

func isNearDuplicate(a, b string) bool {
	an, bn := strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if an == "" || bn == "" {
		return false
	}
	if strings.Contains(an, bn) || strings.Contains(bn, an) {
		return true
	}
	aw, bw := wordSet(an), wordSet(bn)
	if len(aw) == 0 || len(bw) == 0 {
		return false
	}
	overlap := 0
	for w := range aw {
		if bw[w] {
			overlap++
		}
	}
	smaller := len(aw)
	if len(bw) < smaller {
		smaller = len(bw)
	}
	return float64(overlap)/float64(smaller) >= 0.8
}
