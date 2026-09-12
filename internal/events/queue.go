package events

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	StatePending = "pending"
	StateLeased  = "leased"
	StateDone    = "done"
)

type Event struct {
	Source     string          `json:"source"`
	Category   string          `json:"category"`
	ID         string          `json:"id"`
	Payload    json.RawMessage `json:"payload"`
	Attempt    int             `json:"attempt"`
	State      string          `json:"state"`
	LeaseUntil time.Time       `json:"lease_until,omitempty"`
	LeaseToken string          `json:"lease_token,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type Queue struct {
	mu     sync.Mutex
	dir    string
	events map[string]*Event
}

func NewQueue(dir string) (*Queue, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	}
	return q, nil
}

func eventKey(source, id string) string { return source + "\x00" + id }

func (q *Queue) saveLocked() error {
	data, err := json.MarshalIndent(q.events, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(q.dir, "internal_events.json.tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(q.dir, "internal_events.json"))
}

func (q *Queue) Enqueue(source, category, id string, payload json.RawMessage) (bool, error) {
	if source == "" || category == "" || id == "" || !json.Valid(payload) {
		return false, errors.New("source, category, id, and valid payload are required")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	key := eventKey(source, id)
	if _, exists := q.events[key]; exists {
		return false, nil
	}
	now := time.Now().UTC()
	q.events[key] = &Event{Source: source, Category: category, ID: id, Payload: append(json.RawMessage(nil), payload...), State: StatePending, CreatedAt: now, UpdatedAt: now}
	return true, q.saveLocked()
}

func (q *Queue) Lease(now time.Time, duration time.Duration) (*Event, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var candidates []*Event
	for _, event := range q.events {
		if event.State == StatePending || (event.State == StateLeased && !event.LeaseUntil.After(now)) {
			candidates = append(candidates, event)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CreatedAt.Before(candidates[j].CreatedAt) })
	event := candidates[0]
	event.Attempt++
	event.State = StateLeased
	event.LeaseUntil = now.Add(duration)
	event.LeaseToken = randomID()
	event.UpdatedAt = now.UTC()
	if err := q.saveLocked(); err != nil {
		return nil, err
	}
	copy := *event
	copy.Payload = append(json.RawMessage(nil), event.Payload...)
	return &copy, nil
}

func (q *Queue) Complete(event *Event) error {
	return q.finish(event, StateDone)
}

func (q *Queue) Retry(event *Event) error {
	return q.finish(event, StatePending)
}

func (q *Queue) finish(event *Event, state string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	stored := q.events[eventKey(event.Source, event.ID)]
	if stored == nil || stored.State != StateLeased || stored.LeaseToken != event.LeaseToken {
		return errors.New("event lease no longer held")
	}
	stored.State = state
	stored.LeaseToken = ""
	stored.LeaseUntil = time.Time{}
	stored.UpdatedAt = time.Now().UTC()
	return q.saveLocked()
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
