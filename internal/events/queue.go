package events

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/iamangus/eve/internal/durable"
)

const (
	StatePending = "pending"
	StateLeased  = "leased"
	StateDone    = "done"
	StateFailed  = "failed"
)

type Event struct {
	Source        string          `json:"source"`
	Category      string          `json:"category"`
	ID            string          `json:"id"`
	InputID       string          `json:"input_id,omitempty"`
	AgentFailures int             `json:"agent_failures,omitempty"`
	LastError     string          `json:"last_error,omitempty"`
	Payload       json.RawMessage `json:"payload"`
	PayloadDigest string          `json:"payload_digest,omitempty"`
	Attempt       int             `json:"attempt"`
	State         string          `json:"state"`
	LeaseUntil    time.Time       `json:"lease_until,omitempty"`
	NextAttemptAt time.Time       `json:"next_attempt_at,omitempty"`
	LeaseToken    string          `json:"lease_token,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type Queue struct {
	mu     sync.Mutex
	dir    string
	events map[string]*Event
}

func NewQueue(dir string) (*Queue, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	q := &Queue{dir: dir, events: make(map[string]*Event)}
	data, err := os.ReadFile(filepath.Join(dir, "internal_events.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return q, nil
		}
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return q, nil
	}
	if err := json.Unmarshal(data, &q.events); err != nil {
		return nil, err
	}
	for key, event := range q.events {
		if event == nil {
			delete(q.events, key)
			continue
		}
		if event.State == "" {
			event.State = StatePending
		}
		if event.InputID == "" {
			event.InputID = event.ID
		}
		if event.PayloadDigest == "" {
			event.PayloadDigest = payloadDigest(event.Payload)
		}
	}
	return q, nil
}

func eventKey(source, id string) string { return source + "\x00" + id }

func (q *Queue) saveLocked() error {
	return durable.JSON(filepath.Join(q.dir, "internal_events.json"), q.events)
}

func (q *Queue) Enqueue(source, category, id string, payload json.RawMessage) (bool, error) {
	if source == "" || category == "" || id == "" || !json.Valid(payload) {
		return false, errors.New("source, category, id, and valid payload are required")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	key := eventKey(source, id)
	if prior, exists := q.events[key]; exists {
		if prior.Category != category || prior.PayloadDigest != payloadDigest(payload) {
			return false, errors.New("event ID already belongs to a different payload")
		}
		return false, nil
	}
	if prior, err := q.archived(source, id); err == nil {
		if prior.Category != category || prior.PayloadDigest != payloadDigest(payload) {
			return false, errors.New("event ID already belongs to a different payload")
		}
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	now := time.Now().UTC()
	q.events[key] = &Event{Source: source, Category: category, ID: id, InputID: id, Payload: append(json.RawMessage(nil), payload...), PayloadDigest: payloadDigest(payload), State: StatePending, CreatedAt: now, UpdatedAt: now}
	if err := q.saveLocked(); err != nil {
		delete(q.events, key)
		return false, err
	}
	return true, nil
}

func payloadDigest(data json.RawMessage) string {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return ""
	}
	canonical, _ := json.Marshal(value)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func (q *Queue) Status(source, id string) (*Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	event := q.events[eventKey(source, id)]
	if event == nil {
		archived, err := q.archived(source, id)
		return archived, err == nil
	}
	copy := *event
	copy.Payload = append(json.RawMessage(nil), event.Payload...)
	return &copy, true
}

func (q *Queue) Lease(now time.Time, duration time.Duration) (*Event, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var candidates []*Event
	first := make(map[string]*Event)
	for _, event := range q.events {
		if event.State == StatePending || event.State == StateLeased {
			scope := queueScope(event)
			if prior := first[scope]; prior == nil || event.CreatedAt.Before(prior.CreatedAt) || (event.CreatedAt.Equal(prior.CreatedAt) && event.ID < prior.ID) {
				first[scope] = event
			}
		}
	}
	for _, event := range q.events {
		if first[queueScope(event)] != event {
			continue
		}
		if (event.State == StatePending && !event.NextAttemptAt.After(now)) || (event.State == StateLeased && !event.LeaseUntil.After(now)) {
			candidates = append(candidates, event)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CreatedAt.Before(candidates[j].CreatedAt) })
	event := candidates[0]
	before := *event
	event.Attempt++
	event.State = StateLeased
	event.LeaseUntil = now.Add(duration)
	event.LeaseToken = randomID()
	event.NextAttemptAt = time.Time{}
	event.UpdatedAt = now.UTC()
	if err := q.saveLocked(); err != nil {
		*event = before
		return nil, err
	}
	copy := *event
	copy.Payload = append(json.RawMessage(nil), event.Payload...)
	return &copy, nil
}

func queueScope(event *Event) string {
	if event.Category == "backend" {
		return "backend"
	}
	if event.Category == "frontend" {
		var payload struct {
			ConversationID string `json:"conversation_id"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil {
			return "frontend:" + payload.ConversationID
		}
	}
	return eventKey(event.Source, event.ID)
}

func (q *Queue) Complete(event *Event) error {
	return q.finish(event, StateDone)
}

func (q *Queue) Retry(event *Event) error {
	return q.finish(event, StatePending)
}

func (q *Queue) RetryError(event *Event, reason string) error {
	event.LastError = reason
	return q.finish(event, StatePending)
}

func (q *Queue) Snapshot() []Event {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Event, 0, len(q.events))
	for _, event := range q.events {
		copy := *event
		copy.Payload = nil
		out = append(out, copy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

type Metrics struct {
	Pending         int       `json:"pending"`
	Leased          int       `json:"leased"`
	Done            int       `json:"done"`
	Failed          int       `json:"failed"`
	OldestPendingAt time.Time `json:"oldest_pending_at,omitempty"`
}

func (q *Queue) Metrics() Metrics {
	q.mu.Lock()
	defer q.mu.Unlock()
	var metrics Metrics
	for _, event := range q.events {
		switch event.State {
		case StatePending:
			metrics.Pending++
		case StateLeased:
			metrics.Leased++
		case StateDone:
			metrics.Done++
		case StateFailed:
			metrics.Failed++
		}
		if (event.State == StatePending || event.State == StateLeased) && (metrics.OldestPendingAt.IsZero() || event.CreatedAt.Before(metrics.OldestPendingAt)) {
			metrics.OldestPendingAt = event.CreatedAt
		}
	}
	return metrics
}

func (q *Queue) Requeue(source, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	event := q.events[eventKey(source, id)]
	if event == nil {
		return os.ErrNotExist
	}
	if event.State != StateFailed {
		return errors.New("only failed events can be retried")
	}
	before := *event
	event.State = StatePending
	event.AgentFailures = 0
	event.InputID = randomID()
	event.NextAttemptAt = time.Time{}
	event.UpdatedAt = time.Now().UTC()
	if err := q.saveLocked(); err != nil {
		*event = before
		return err
	}
	return nil
}

func (q *Queue) Fail(event *Event, reason string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	stored := q.events[eventKey(event.Source, event.ID)]
	if stored == nil || stored.State != StateLeased || stored.LeaseToken != event.LeaseToken {
		return errors.New("event lease no longer held")
	}
	before := *stored
	stored.State = StateFailed
	stored.LastError = reason
	stored.LeaseToken = ""
	stored.LeaseUntil = time.Time{}
	stored.NextAttemptAt = time.Time{}
	stored.UpdatedAt = time.Now().UTC()
	if err := q.saveLocked(); err != nil {
		*stored = before
		return err
	}
	return nil
}

func (q *Queue) AgentInputFailed(event *Event, reason string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	stored := q.events[eventKey(event.Source, event.ID)]
	if stored == nil || stored.State != StateLeased || stored.LeaseToken != event.LeaseToken {
		return errors.New("event lease no longer held")
	}
	before := *stored
	stored.AgentFailures++
	stored.LastError = reason
	stored.LeaseToken = ""
	stored.LeaseUntil = time.Time{}
	stored.UpdatedAt = time.Now().UTC()
	if stored.AgentFailures >= 5 {
		stored.State = StateFailed
		stored.NextAttemptAt = time.Time{}
	} else {
		stored.State = StatePending
		stored.InputID = fmt.Sprintf("%s-retry-%d", stored.ID, stored.AgentFailures)
		stored.NextAttemptAt = stored.UpdatedAt.Add(time.Second << min(stored.AgentFailures-1, 6))
	}
	if err := q.saveLocked(); err != nil {
		*stored = before
		return err
	}
	return nil
}

func (q *Queue) finish(event *Event, state string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	stored := q.events[eventKey(event.Source, event.ID)]
	if stored == nil || stored.State != StateLeased || stored.LeaseToken != event.LeaseToken {
		return errors.New("event lease no longer held")
	}
	before := *stored
	stored.State = state
	stored.LastError = event.LastError
	if state == StateDone {
		stored.Payload = nil
	}
	stored.LeaseToken = ""
	stored.LeaseUntil = time.Time{}
	stored.UpdatedAt = time.Now().UTC()
	if state == StatePending {
		backoff := time.Second << min(stored.Attempt-1, 6)
		stored.NextAttemptAt = stored.UpdatedAt.Add(backoff)
	} else {
		stored.NextAttemptAt = time.Time{}
	}
	if err := q.saveLocked(); err != nil {
		*stored = before
		return err
	}
	return nil
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b)
}

// NewID supplies durable callers with an opaque event identifier.
func NewID() string { return randomID() }
