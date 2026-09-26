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
	if err != nil {
		delete(s.convs, id)
	}
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
		if s.deleting[id] {
			continue
		}
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
	if s.deleting[id] {
		s.mu.RUnlock()
		return nil, ErrDeleteInProgress
	}
	c := Conversation{
		ID:                id,
		Title:             r.Title,
		CreatedAt:         r.Created,
		UpdatedAt:         r.Updated,
		ActiveRunID:       r.ActiveRunID,
		ActiveMessageID:   r.ActiveMessageID,
		ActiveSubmitted:   r.ActiveSubmitted,
		DeliveryFailures:  r.DeliveryFailures,
		PendingMessageIDs: append([]int64(nil), r.PendingOwnerInputs...),
		SummarizedUpTo:    s.boundaryLocked(id),
	}
	for _, steer := range r.PendingSteers {
		c.PendingMessageIDs = append(c.PendingMessageIDs, steer.MessageID)
	}
	for _, steer := range r.ConfirmedSteers {
		c.SteeredMessageIDs = append(c.SteeredMessageIDs, steer.MessageID)
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
	prior, ok := s.convs[id]
	compartments, hasCompartments := s.compartments[id]
	if !ok && !hasCompartments {
		return ErrNotFound
	}
	if ok {
		delete(s.convs, id)
		if err := s.saveConversations(); err != nil {
			s.convs[id] = prior
			return err
		}
	}
	if hasCompartments {
		delete(s.compartments, id)
		if err := s.saveCompartments(); err != nil {
			s.compartments[id] = compartments
			return err
		}
	}
	return nil
}

func (s *Store) BeginConversationDelete(id string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.convs[id]
	if r == nil {
		return nil, ErrNotFound
	}
	if s.deleting[id] {
		return nil, ErrDeleteInProgress
	}
	s.deleting[id] = true
	seen := make(map[string]bool)
	var runIDs []string
	if r.ActiveRunID != "" {
		runIDs = append(runIDs, r.ActiveRunID)
		seen[r.ActiveRunID] = true
	}
	for runID := range r.ActiveRunMessages {
		if !seen[runID] {
			runIDs = append(runIDs, runID)
		}
	}
	return runIDs, nil
}

func (s *Store) EndConversationDelete(id string) {
	s.mu.Lock()
	delete(s.deleting, id)
	s.mu.Unlock()
}

func (s *Store) AppendUserMessage(convID, content, channel, sender string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok || s.deleting[convID] {
		return ErrNotFound
	}
	before := cloneConversationRecord(r)
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
	return s.saveConversationLocked(convID, before)
}

// AppendUserMessageReturn appends a user message and returns the stored
// message so callers (channel adapters) can broadcast its full shape.
// threadRef and replyTo capture where a response must be mechanically
// delivered (e.g. a Matrix room ID or the sender's email address).
func (s *Store) AppendUserMessageReturn(convID, content, channel, sender, threadRef, replyTo string) (*Message, error) {
	return s.appendUserMessage(convID, content, channel, sender, threadRef, replyTo)
}

func (s *Store) AppendQueuedOwnerMessageWithSource(convID, content, channel, sender, threadRef, replyTo, sourceID string) (*Message, bool, error) {
	m, created, _, err := s.appendOwnerMessage(convID, content, channel, sender, threadRef, replyTo, sourceID, "")
	return m, created, err
}

func (s *Store) AppendSteeredOwnerMessageWithSource(convID, content, channel, sender, threadRef, replyTo, sourceID, runID string) (*Message, bool, string, error) {
	return s.appendOwnerMessage(convID, content, channel, sender, threadRef, replyTo, sourceID, runID)
}

func (s *Store) appendOwnerMessage(convID, content, channel, sender, threadRef, replyTo, sourceID, steerRun string) (*Message, bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok || s.deleting[convID] {
		return nil, false, "", ErrNotFound
	}
	if sourceID != "" {
		for _, prior := range r.Messages {
			if prior.Role == "user" && prior.Channel == channel && prior.SourceID == sourceID {
				if prior.Content != content || prior.Sender != sender || prior.ThreadRef != threadRef || prior.ReplyTo != replyTo {
					return nil, false, "", ErrSourceConflict
				}
				copy := prior
				for _, steer := range r.PendingSteers {
					if steer.MessageID == prior.ID {
						return &copy, false, steer.RunID, nil
					}
				}
				for _, steer := range r.ConfirmedSteers {
					if steer.MessageID == prior.ID {
						return &copy, false, steer.RunID, nil
					}
				}
				return &copy, false, "", nil
			}
		}
	}
	before := cloneConversationRecord(r)
	now := time.Now()
	m := Message{ID: r.NextMsgID, Role: "user", Content: content, Channel: channel, Sender: sender, ThreadRef: threadRef, ReplyTo: replyTo, SourceID: sourceID, CreatedAt: now}
	r.Messages = append(r.Messages, m)
	anchorID := r.ActiveMessageID
	if tracked, ok := r.ActiveRunMessages[steerRun]; ok {
		anchorID = tracked
	}
	steerable := steerRun != "" && steerRun == r.ActiveRunID && r.ActiveSubmitted && channel == "web"
	if steerable {
		steerable = false
		for _, prior := range r.Messages {
			if prior.ID == anchorID && prior.Role == "user" && (prior.Channel == "" || prior.Channel == "web") {
				steerable = true
				break
			}
		}
	}
	if steerable {
		r.PendingSteers = append(r.PendingSteers, SteeringInput{MessageID: m.ID, RunID: steerRun, AnchorID: anchorID})
	} else {
		r.PendingOwnerInputs = append(r.PendingOwnerInputs, m.ID)
	}
	r.NextMsgID++
	r.Updated = now
	if err := s.saveConversationLocked(convID, before); err != nil {
		return nil, false, "", err
	}
	if steerable {
		return &m, true, steerRun, nil
	}
	return &m, true, "", nil
}

func (s *Store) PendingSteersForRun(convID, runID string) []SteeringInput {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.convs[convID]
	if r == nil || s.deleting[convID] {
		return nil
	}
	var out []SteeringInput
	for _, steer := range r.PendingSteers {
		if steer.RunID == runID {
			out = append(out, steer)
		}
	}
	return out
}

func (s *Store) PendingSteer(convID, runID string, messageID int64) (SteeringInput, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.convs[convID]
	if r == nil || s.deleting[convID] {
		return SteeringInput{}, false
	}
	for _, steer := range r.PendingSteers {
		if steer.RunID == runID && steer.MessageID == messageID {
			return steer, true
		}
	}
	return SteeringInput{}, false
}

func (s *Store) SteeringMessage(convID string, steer SteeringInput) (*Message, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.convs[convID]
	if r == nil || s.deleting[convID] {
		return nil, false
	}
	for _, msg := range r.Messages {
		if msg.ID == steer.MessageID && msg.Role == "user" {
			copy := msg
			return &copy, r.ActiveRunID == steer.RunID && r.ActiveMessageID == steer.AnchorID
		}
	}
	return nil, false
}

func (s *Store) ConfirmedSteer(convID, runID string, messageID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.convs[convID]
	if r == nil {
		return false
	}
	for _, steer := range r.ConfirmedSteers {
		if steer.RunID == runID && steer.MessageID == messageID {
			return true
		}
	}
	return false
}

func (s *Store) ConfirmSteer(convID, runID string, messageID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.convs[convID]
	if r == nil || s.deleting[convID] {
		return ErrNotFound
	}
	for _, steer := range r.ConfirmedSteers {
		if steer.RunID == runID && steer.MessageID == messageID {
			return nil
		}
	}
	for i, steer := range r.PendingSteers {
		if steer.RunID == runID && steer.MessageID == messageID {
			before := cloneConversationRecord(r)
			r.PendingSteers = append(r.PendingSteers[:i], r.PendingSteers[i+1:]...)
			r.ConfirmedSteers = append(r.ConfirmedSteers, steer)
			return s.saveConversationLocked(convID, before)
		}
	}
	return ErrNotFound
}

func (s *Store) QueueSteer(convID, runID string, messageID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.convs[convID]
	if r == nil || s.deleting[convID] {
		return ErrNotFound
	}
	for i, steer := range r.PendingSteers {
		if steer.RunID == runID && steer.MessageID == messageID {
			before := cloneConversationRecord(r)
			r.PendingSteers = append(r.PendingSteers[:i], r.PendingSteers[i+1:]...)
			queueOwnerInput(r, messageID)
			return s.saveConversationLocked(convID, before)
		}
	}
	return ErrNotFound
}

func (s *Store) RequeueSteersForRun(convID, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.convs[convID]
	if r == nil || s.deleting[convID] {
		return ErrNotFound
	}
	before := cloneConversationRecord(r)
	for _, steer := range r.PendingSteers {
		if steer.RunID == runID {
			queueOwnerInput(r, steer.MessageID)
		}
	}
	for _, steer := range r.ConfirmedSteers {
		if steer.RunID == runID {
			queueOwnerInput(r, steer.MessageID)
		}
	}
	r.PendingSteers = removeSteersForRun(r.PendingSteers, runID)
	r.ConfirmedSteers = removeSteersForRun(r.ConfirmedSteers, runID)
	return s.saveConversationLocked(convID, before)
}

func queueOwnerInput(r *conversationRecord, messageID int64) {
	for i, id := range r.PendingOwnerInputs {
		if id == messageID {
			return
		}
		if id > messageID {
			r.PendingOwnerInputs = append(r.PendingOwnerInputs, 0)
			copy(r.PendingOwnerInputs[i+1:], r.PendingOwnerInputs[i:])
			r.PendingOwnerInputs[i] = messageID
			return
		}
	}
	r.PendingOwnerInputs = append(r.PendingOwnerInputs, messageID)
}

// SteerTarget returns the run that a new web message should be injected into,
// or "" when it must queue instead: no turn is in flight, the active input has
// not been submitted yet, or the active turn would reply on a non-web channel
// (the shared response must keep the originating message's reply destination).
func (s *Store) SteerTarget(convID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.convs[convID]
	if !ok || s.deleting[convID] {
		return ""
	}
	if r.ActiveRunID == "" || !r.ActiveSubmitted {
		return ""
	}
	msgID := r.ActiveMessageID
	if tracked, ok := r.ActiveRunMessages[r.ActiveRunID]; ok {
		msgID = tracked
	}
	for _, m := range r.Messages {
		if m.ID == msgID && m.Role == "user" {
			if m.Channel != "" && m.Channel != "web" {
				return ""
			}
			break
		}
	}
	return r.ActiveRunID
}

func (s *Store) appendUserMessage(convID, content, channel, sender, threadRef, replyTo string) (*Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok || s.deleting[convID] {
		return nil, ErrNotFound
	}
	before := cloneConversationRecord(r)
	now := time.Now()
	m := Message{
		ID:        r.NextMsgID,
		Role:      "user",
		Content:   content,
		Channel:   channel,
		Sender:    sender,
		ThreadRef: threadRef,
		ReplyTo:   replyTo,
		CreatedAt: now,
	}
	r.Messages = append(r.Messages, m)
	r.NextMsgID++
	r.Updated = now
	if err := s.saveConversationLocked(convID, before); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) PendingOwnerMessage(convID string) (*Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.convs[convID]
	if !ok || s.deleting[convID] {
		return nil, ErrNotFound
	}
	if r.ActiveRunID != "" || len(r.PendingOwnerInputs) == 0 {
		return nil, nil
	}
	for _, m := range r.Messages {
		if m.ID == r.PendingOwnerInputs[0] {
			copy := m
			return &copy, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) PendingOwnerConversations() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ids []string
	for id, r := range s.convs {
		if s.deleting[id] {
			continue
		}
		if len(r.PendingOwnerInputs) > 0 && r.ActiveRunID == "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Store) ClaimPendingOwnerMessage(convID, runID string, messageID int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.convs[convID]
	if r == nil || s.deleting[convID] {
		return false, ErrNotFound
	}
	if r.ActiveRunID != "" || len(r.PendingOwnerInputs) == 0 || r.PendingOwnerInputs[0] != messageID {
		return false, nil
	}
	before := cloneConversationRecord(r)
	r.PendingOwnerInputs = r.PendingOwnerInputs[1:]
	r.ActiveRunID = runID
	r.ActiveMessageID = messageID
	r.ActiveSubmitted = false
	r.DeliveryFailures = 0
	r.NextDeliveryAt = time.Time{}
	if r.ActiveRunMessages == nil {
		r.ActiveRunMessages = make(map[string]int64)
	}
	r.ActiveRunMessages[runID] = messageID
	if err := s.saveConversationLocked(convID, before); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ReplaceActiveOwnerRun(convID, oldRunID, newRunID string, messageID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.convs[convID]
	if r == nil || r.ActiveRunID != oldRunID || r.ActiveMessageID != messageID {
		return ErrNotFound
	}
	before := cloneConversationRecord(r)
	for _, steer := range r.PendingSteers {
		if steer.RunID == oldRunID {
			queueOwnerInput(r, steer.MessageID)
		}
	}
	for _, steer := range r.ConfirmedSteers {
		if steer.RunID == oldRunID {
			queueOwnerInput(r, steer.MessageID)
		}
	}
	r.PendingSteers = removeSteersForRun(r.PendingSteers, oldRunID)
	r.ConfirmedSteers = removeSteersForRun(r.ConfirmedSteers, oldRunID)
	delete(r.ActiveRunMessages, oldRunID)
	r.ActiveRunID = newRunID
	r.ActiveSubmitted = false
	r.ActiveRunMessages[newRunID] = messageID
	return s.saveConversationLocked(convID, before)
}

func removeSteersForRun(steers []SteeringInput, runID string) []SteeringInput {
	out := steers[:0]
	for _, steer := range steers {
		if steer.RunID != runID {
			out = append(out, steer)
		}
	}
	return out
}

func (s *Store) ActiveOwnerMessage(convID, runID string) (*Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.convs[convID]
	if r == nil {
		return nil, ErrNotFound
	}
	id, tracked := r.ActiveRunMessages[runID]
	if !tracked && r.ActiveRunID == runID {
		id = r.ActiveMessageID
	}
	for _, m := range r.Messages {
		if m.ID == id && m.Role == "user" {
			copy := m
			return &copy, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) MarkActiveSubmitted(convID, runID string, messageID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.convs[convID]
	if r == nil || r.ActiveRunID != runID || r.ActiveMessageID != messageID {
		return ErrNotFound
	}
	if r.ActiveSubmitted {
		return nil
	}
	before := cloneConversationRecord(r)
	r.ActiveSubmitted = true
	return s.saveConversationLocked(convID, before)
}

func (s *Store) AppendAssistantMessage(convID, runID, content, channel, sender string) error {
	_, _, err := s.AppendAssistantForTurn(convID, runID, "", content, channel, sender)
	return err
}

func (s *Store) HasAssistantForTurn(convID, turnID string) bool {
	if turnID == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if r := s.convs[convID]; r != nil {
		for _, m := range r.Messages {
			if m.Role == "assistant" && m.TurnID == turnID {
				return true
			}
		}
	}
	return false
}

func (s *Store) AppendAssistantForTurn(convID, runID, turnID, content, channel, sender string) (*Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok || s.deleting[convID] {
		return nil, false, ErrNotFound
	}
	if turnID != "" {
		for _, prior := range r.Messages {
			if prior.Role == "assistant" && prior.TurnID == turnID {
				copy := prior
				return &copy, false, nil
			}
		}
	}
	before := cloneConversationRecord(r)
	now := time.Now()
	m := Message{
		ID:        r.NextMsgID,
		Role:      "assistant",
		Content:   content,
		RunID:     runID,
		TurnID:    turnID,
		Channel:   channel,
		Sender:    sender,
		CreatedAt: now,
	}
	r.Messages = append(r.Messages, m)
	r.NextMsgID++
	r.Updated = now
	if err := s.saveConversationLocked(convID, before); err != nil {
		return nil, false, err
	}
	return &m, true, nil
}

func (s *Store) AppendAssistantMessageReturn(convID, runID, content, channel, sender string) (*Message, error) {
	m, _, err := s.AppendAssistantForTurn(convID, runID, "", content, channel, sender)
	return m, err
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

func (s *Store) SetActiveRunForMessage(convID, runID string, messageID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return ErrNotFound
	}
	before := cloneConversationRecord(r)
	r.ActiveRunID = runID
	r.ActiveMessageID = messageID
	r.ActiveSubmitted = true
	if r.ActiveRunMessages == nil {
		r.ActiveRunMessages = make(map[string]int64)
	}
	r.ActiveRunMessages[runID] = messageID
	r.Updated = time.Now()
	return s.saveConversationLocked(convID, before)
}

func (s *Store) ActiveRunReply(convID, runID string) (channel, threadRef, replyTo string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.convs[convID]
	if !ok {
		return "", "", "", ErrNotFound
	}
	messageID, tracked := r.ActiveRunMessages[runID]
	if !tracked && r.ActiveRunID != runID {
		return "", "", "", ErrNotFound
	}
	if !tracked {
		messageID = r.ActiveMessageID
	}
	for _, m := range r.Messages {
		if m.ID == messageID && m.Role == "user" && messageID != 0 {
			if m.Channel == "" {
				return "web", m.ThreadRef, m.ReplyTo, nil
			}
			return m.Channel, m.ThreadRef, m.ReplyTo, nil
		}
	}
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if m := r.Messages[i]; m.Role == "user" {
			if m.Channel == "" {
				return "web", m.ThreadRef, m.ReplyTo, nil
			}
			return m.Channel, m.ThreadRef, m.ReplyTo, nil
		}
	}
	return "web", "", "", nil
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

func (s *Store) ClearActiveRunID(convID, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return ErrNotFound
	}
	for _, steer := range r.PendingSteers {
		if steer.RunID == runID {
			return ErrSteerPending
		}
	}
	before := cloneConversationRecord(r)
	r.ConfirmedSteers = removeSteersForRun(r.ConfirmedSteers, runID)
	delete(r.ActiveRunMessages, runID)
	if r.ActiveRunID == runID {
		r.ActiveRunID = ""
		r.ActiveMessageID = 0
		r.ActiveSubmitted = false
		r.DeliveryFailures = 0
		r.NextDeliveryAt = time.Time{}
		for id, messageID := range r.ActiveRunMessages {
			if messageID >= r.ActiveMessageID {
				r.ActiveRunID = id
				r.ActiveMessageID = messageID
			}
		}
	}
	r.Updated = time.Now()
	return s.saveConversationLocked(convID, before)
}

func (s *Store) DeliveryReady(convID, runID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.convs[convID]
	if r == nil || s.deleting[convID] || time.Now().Before(r.NextDeliveryAt) {
		return false
	}
	_, tracked := r.ActiveRunMessages[runID]
	return r.ActiveRunID == runID || tracked
}

func (s *Store) RecordDeliveryFailure(convID, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.convs[convID]
	if r == nil || s.deleting[convID] {
		return ErrNotFound
	}
	_, tracked := r.ActiveRunMessages[runID]
	if r.ActiveRunID != runID && !tracked {
		return ErrNotFound
	}
	before := cloneConversationRecord(r)
	r.DeliveryFailures++
	exponent := min(r.DeliveryFailures, 6)
	r.NextDeliveryAt = time.Now().Add(time.Duration(1<<exponent) * time.Second)
	return s.saveConversationLocked(convID, before)
}

func (s *Store) SetTitle(convID, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.convs[convID]
	if !ok {
		return ErrNotFound
	}
	before := cloneConversationRecord(r)
	r.Title = title
	return s.saveConversationLocked(convID, before)
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

type ActiveRun struct {
	ConversationID string
	RunID          string
}

func (s *Store) ActiveRuns() ([]ActiveRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ActiveRun
	for id, r := range s.convs {
		if s.deleting[id] {
			continue
		}
		for runID := range r.ActiveRunMessages {
			out = append(out, ActiveRun{ConversationID: id, RunID: runID})
		}
		if r.ActiveRunID != "" {
			if _, ok := r.ActiveRunMessages[r.ActiveRunID]; !ok {
				out = append(out, ActiveRun{ConversationID: id, RunID: r.ActiveRunID})
			}
		}
	}
	return out, nil
}
