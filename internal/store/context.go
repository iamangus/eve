package store

import (
	"time"
)

// Fact is a durable piece of knowledge extracted from a conversation chunk.
type Fact struct {
	Category string `json:"category"`
	Content  string `json:"content"`
}

// CompartmentTiers holds four paraphrase levels of the same summary,
// decreasing in length: P1 (verbose) through P4 (anchor-only).
type CompartmentTiers struct {
	P1 string `json:"p1"`
	P2 string `json:"p2"`
	P3 string `json:"p3"`
	P4 string `json:"p4"`
}

// Compartment covers the raw messages [StartMsgID, EndMsgID] which have been
// compressed into tiered summaries. Raw messages are retained.
type Compartment struct {
	ID         string           `json:"id"`
	StartMsgID int64            `json:"start_msg_id"`
	EndMsgID   int64            `json:"end_msg_id"`
	CreatedAt  time.Time        `json:"created_at"`
	Importance int              `json:"importance"`
	Tiers      CompartmentTiers `json:"tiers"`
	Facts      []Fact           `json:"facts,omitempty"`
}

// Memory is durable cross-conversation knowledge captured from facts.
type Memory struct {
	ID                string    `json:"id"`
	Category          string    `json:"category"`
	Content           string    `json:"content"`
	Importance        int       `json:"importance"`
	CreatedAt         time.Time `json:"created_at"`
	SourceCompartment string    `json:"source_compartment,omitempty"`
	Hash              string    `json:"hash,omitempty"`
}

// boundaryLocked returns the message ID up to which this conversation has been
// summarized (the last compartment's EndMsgID), or 0 when nothing is summarized.
// Callers must hold s.mu.
func (s *Store) boundaryLocked(convID string) int64 {
	comps := s.compartments[convID]
	if len(comps) == 0 {
		return 0
	}
	return comps[len(comps)-1].EndMsgID
}

func (s *Store) SummarizedUpTo(convID string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.convs[convID]; !ok {
		return 0, ErrNotFound
	}
	return s.boundaryLocked(convID), nil
}

func (s *Store) Compartments(convID string) []Compartment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Compartment(nil), s.compartments[convID]...)
}

func (s *Store) AddCompartment(convID string, c Compartment) (Compartment, error) {
	if c.ID == "" {
		c.ID = newID()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compartments[convID] = append(s.compartments[convID], c)
	if err := s.saveCompartments(); err != nil {
		return Compartment{}, err
	}
	return c, nil
}

func (s *Store) saveCompartments() error {
	return s.saveJSON("compartments.json", s.compartments)
}

func (s *Store) Memories() []Memory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Memory(nil), s.memories...)
}

func (s *Store) MemoryByHash(hash string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.memories {
		if m.Hash == hash {
			return true
		}
	}
	return false
}

func (s *Store) AddMemory(m Memory) error {
	if m.ID == "" {
		m.ID = newID()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memories = append(s.memories, m)
	return s.saveMemories()
}

// ReplaceMemories swaps the whole memory pool (used by curation).
func (s *Store) ReplaceMemories(mems []Memory) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memories = append([]Memory(nil), mems...)
	return s.saveMemories()
}

func (s *Store) saveMemories() error {
	return s.saveJSON("memories.json", s.memories)
}

func (s *Store) SetHistorianState(convID string, runAt time.Time, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return ErrNotFound
	}
	r.HistorianRunAt = runAt
	r.HistorianError = errMsg
	return s.saveConversations()
}

func (s *Store) HistorianState(convID string) (runAt time.Time, errMsg string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.convs[convID]
	if !ok {
		return time.Time{}, "", ErrNotFound
	}
	return r.HistorianRunAt, r.HistorianError, nil
}
