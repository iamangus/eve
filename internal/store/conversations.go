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
		Title:        title,
		Created:      now,
		Updated:      now,
		NextMsgID:    1,
		Participants: []string{"owner"},
	}
	s.mu.Lock()
	s.convs[id] = rec
	err := s.saveConversations()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &Conversation{ID: id, Title: title, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) ListConversations() []ConversationSummary {
	s.mu.RLock()
	out := make([]ConversationSummary, 0, len(s.convs))
	for id, r := range s.convs {
		out = append(out, ConversationSummary{
			ID:           id,
			Title:        r.Title,
			UpdatedAt:    r.Updated,
			MessageCount: len(r.Messages),
			ActiveRunID:  r.ActiveRunID,
		})
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

// PrimaryConversationID returns the ID of the earliest-created conversation,
// or "" when none exists. With the single-conversation UI this is the one
// conversation the user talks to.
func (s *Store) PrimaryConversationID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var bestID string
	var bestCreated time.Time
	for id, r := range s.convs {
		if bestID == "" || r.Created.Before(bestCreated) {
			bestID = id
			bestCreated = r.Created
		}
	}
	return bestID
}

func (s *Store) GetConversation(id string) (*Conversation, error) {
	s.mu.RLock()
	r, ok := s.convs[id]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrNotFound
	}
	c := Conversation{
		ID:            id,
		Title:         r.Title,
		CreatedAt:     r.Created,
		UpdatedAt:     r.Updated,
		ActiveRunID:   r.ActiveRunID,
		SummarizedUpTo: s.boundaryLocked(id),
	}
	if len(r.Messages) > 0 {
		c.Messages = append([]Message(nil), r.Messages...)
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
	if err := s.saveConversations(); err != nil {
		return err
	}
	return s.saveCompartments()
}

func (s *Store) AppendUserMessage(convID, content, channel, sender string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return ErrNotFound
	}
	now := time.Now()
	r.Messages = append(r.Messages, Message{
		ID:        r.NextMsgID,
		Role:      "user",
		Content:   content,
		Channel:   channel,
		Sender:    sender,
		CreatedAt: now,
	})
	r.NextMsgID++
	r.Updated = now
	return s.saveConversations()
}

// AppendUserMessageReturn appends a user message and returns the stored
// message so callers (channel adapters) can broadcast its full shape.
func (s *Store) AppendUserMessageReturn(convID, content, channel, sender string) (*Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return nil, ErrNotFound
	}
	now := time.Now()
	m := Message{
		ID:        r.NextMsgID,
		Role:      "user",
		Content:   content,
		Channel:   channel,
		Sender:    sender,
		CreatedAt: now,
	}
	r.Messages = append(r.Messages, m)
	r.NextMsgID++
	r.Updated = now
	if err := s.saveConversations(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) AppendAssistantMessage(convID, runID, content, channel, sender string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return ErrNotFound
	}
	now := time.Now()
	r.Messages = append(r.Messages, Message{
		ID:        r.NextMsgID,
		Role:      "assistant",
		Content:   content,
		RunID:     runID,
		Channel:   channel,
		Sender:    sender,
		CreatedAt: now,
	})
	r.NextMsgID++
	r.Updated = now
	return s.saveConversations()
}

// AppendAssistantMessageReturn appends an assistant message and returns the
// stored message so callers can broadcast its full shape.
func (s *Store) AppendAssistantMessageReturn(convID, runID, content, channel, sender string) (*Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return nil, ErrNotFound
	}
	now := time.Now()
	m := Message{
		ID:        r.NextMsgID,
		Role:      "assistant",
		Content:   content,
		RunID:     runID,
		Channel:   channel,
		Sender:    sender,
		CreatedAt: now,
	}
	r.Messages = append(r.Messages, m)
	r.NextMsgID++
	r.Updated = now
	if err := s.saveConversations(); err != nil {
		return nil, err
	}
	return &m, nil
}

// ConversationParticipants returns the identity names participating in a
// conversation. It defaults to just the owner.
func (s *Store) ConversationParticipants(convID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.convs[convID]
	if !ok {
		return nil, ErrNotFound
	}
	if len(r.Participants) == 0 {
		return []string{"owner"}, nil
	}
	return append([]string(nil), r.Participants...), nil
}

func (s *Store) ConversationHistory(convID string) ([]Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.convs[convID]
	if !ok {
		return nil, ErrNotFound
	}
	if len(r.Messages) == 0 {
		return []Message{}, nil
	}
	return append([]Message(nil), r.Messages...), nil
}

// LastUserChannel returns the channel of the most recent user message, or
// "web" when there is none. Used to tag run responses with the conversation's
// origin channel instead of hardcoding "web".
func (s *Store) LastUserChannel(convID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.convs[convID]
	if !ok {
		return "", ErrNotFound
	}
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == "user" {
			if ch := r.Messages[i].Channel; ch != "" {
				return ch, nil
			}
			return "web", nil
		}
	}
	return "web", nil
}

func (s *Store) SetActiveRun(convID, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return ErrNotFound
	}
	r.ActiveRunID = runID
	r.Updated = time.Now()
	return s.saveConversations()
}

// ActiveRunForConversation returns the run id currently in flight for a
// conversation, or "" when it is idle.
func (s *Store) ActiveRunForConversation(convID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.convs[convID]
	if !ok {
		return "", ErrNotFound
	}
	return r.ActiveRunID, nil
}

func (s *Store) ClearActiveRun(convID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return ErrNotFound
	}
	r.ActiveRunID = ""
	r.Updated = time.Now()
	return s.saveConversations()
}

func (s *Store) SetTitle(convID, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return ErrNotFound
	}
	r.Title = title
	return s.saveConversations()
}

func (s *Store) UserMessageCount(convID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.convs[convID]
	if !ok {
		return 0, ErrNotFound
	}
	n := 0
	for _, m := range r.Messages {
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
		if r.ActiveRunID == runID {
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
		if r.ActiveRunID != "" {
			out[id] = r.ActiveRunID
		}
	}
	return out, nil
}
