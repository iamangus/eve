package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b)
}

func (s *Store) CreateConversation(title string) (*Conversation, error) {
	if title == "" {
		title = "New chat"
	}
	id := newID()
	now := time.Now()
	rec := &conversationRecord{
		title:     title,
		created:   now,
		updated:   now,
		nextMsgID: 1,
	}
	s.mu.Lock()
	s.convs[id] = rec
	s.mu.Unlock()
	return &Conversation{ID: id, Title: title, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) ListConversations() []ConversationSummary {
	s.mu.RLock()
	out := make([]ConversationSummary, 0, len(s.convs))
	for id, r := range s.convs {
		out = append(out, ConversationSummary{
			ID:           id,
			Title:        r.title,
			UpdatedAt:    r.updated,
			MessageCount: len(r.messages),
			ActiveRunID:  r.activeRunID,
		})
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (s *Store) GetConversation(id string) (*Conversation, error) {
	s.mu.RLock()
	r, ok := s.convs[id]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrNotFound
	}
	c := Conversation{
		ID:          id,
		Title:       r.title,
		CreatedAt:   r.created,
		UpdatedAt:   r.updated,
		ActiveRunID: r.activeRunID,
	}
	if len(r.messages) > 0 {
		c.Messages = append([]Message(nil), r.messages...)
	}
	s.mu.RUnlock()
	return &c, nil
}

func (s *Store) DeleteConversation(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.convs[id]; !ok {
		return ErrNotFound
	}
	delete(s.convs, id)
	return nil
}

func (s *Store) AppendUserMessage(convID, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return ErrNotFound
	}
	now := time.Now()
	r.messages = append(r.messages, Message{
		ID:        r.nextMsgID,
		Role:      "user",
		Content:   content,
		CreatedAt: now,
	})
	r.nextMsgID++
	r.updated = now
	return nil
}

func (s *Store) AppendAssistantMessage(convID, runID, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return ErrNotFound
	}
	now := time.Now()
	r.messages = append(r.messages, Message{
		ID:        r.nextMsgID,
		Role:      "assistant",
		Content:   content,
		RunID:     runID,
		CreatedAt: now,
	})
	r.nextMsgID++
	r.updated = now
	return nil
}

func (s *Store) ConversationHistory(convID string) ([]Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.convs[convID]
	if !ok {
		return nil, ErrNotFound
	}
	if len(r.messages) == 0 {
		return []Message{}, nil
	}
	return append([]Message(nil), r.messages...), nil
}

func (s *Store) SetActiveRun(convID, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return ErrNotFound
	}
	r.activeRunID = runID
	r.updated = time.Now()
	return nil
}

func (s *Store) ClearActiveRun(convID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return ErrNotFound
	}
	r.activeRunID = ""
	r.updated = time.Now()
	return nil
}

func (s *Store) SetTitle(convID, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return ErrNotFound
	}
	r.title = title
	return nil
}

func (s *Store) UserMessageCount(convID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.convs[convID]
	if !ok {
		return 0, ErrNotFound
	}
	n := 0
	for _, m := range r.messages {
		if m.Role == "user" {
			n++
		}
	}
	return n, nil
}

func (s *Store) ConversationByActiveRun(runID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, r := range s.convs {
		if r.activeRunID == runID {
			return id, nil
		}
	}
	return "", nil
}

func (s *Store) ActiveRuns() (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string)
	for id, r := range s.convs {
		if r.activeRunID != "" {
			out[id] = r.activeRunID
		}
	}
	return out, nil
}