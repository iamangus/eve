package tasks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Task statuses. A task runs as a child agent in agentfoundry; the poller
// advances it to a terminal state or to needs_input (the child requested
// information from the user via structured output).
const (
	StatusRunning   = "running"
	StatusNeedsInput = "needs_input"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

var ErrNotFound = errors.New("task not found")

// Task is a background subtask Eve spawned. It is a mini-conversation: the
// child agent may reply multiple times, and the user may reply back through
// reply_task, which re-runs the child with the new input.
type Task struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	AgentID        string    `json:"agent_id"`
	AgentName      string    `json:"agent_name"`
	Message        string    `json:"message"`
	Replies        []Reply   `json:"replies,omitempty"`
	Status         string    `json:"status"`
	RunID          string    `json:"run_id,omitempty"`
	Result         string    `json:"result,omitempty"`
	Question       string    `json:"question,omitempty"`
	Reported       bool      `json:"reported,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Reply struct {
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

func (t Task) Active() bool {
	return t.Status == StatusRunning || t.Status == StatusNeedsInput
}

func (t Task) Terminal() bool {
	switch t.Status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// Store persists tasks to tasks.json atomically. Safe for concurrent use.
type Store struct {
	mu    sync.RWMutex
	dir   string
	tasks map[string]*Task
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, tasks: make(map[string]*Task)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(filepath.Join(s.dir, "tasks.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var raw map[string]*Task
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for id, t := range raw {
		if t != nil {
			s.tasks[id] = t
		}
	}
	return nil
}

func (s *Store) save() error {
	data, err := json.MarshalIndent(s.tasks, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, "tasks.json.tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, "tasks.json"))
}

func (s *Store) Create(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[t.ID] = t
	return s.save()
}

func (s *Store) Get(id string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	return *t, nil
}

func (s *Store) List() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (s *Store) ListByConversation(convID string) []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, 0)
	for _, t := range s.tasks {
		if t.ConversationID == convID {
			out = append(out, *t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Active returns tasks that are still running or awaiting input.
func (s *Store) Active() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, 0)
	for _, t := range s.tasks {
		if t.Active() {
			out = append(out, *t)
		}
	}
	return out
}

// Unreported returns tasks in a state that should be surfaced to the user
// (terminal or awaiting input) that have not yet been reported on.
func (s *Store) Unreported() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, 0)
	for _, t := range s.tasks {
		if !t.Reported && (t.Terminal() || t.Status == StatusNeedsInput) {
			out = append(out, *t)
		}
	}
	return out
}

func (s *Store) update(id string, fn func(*Task) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return ErrNotFound
	}
	if err := fn(t); err != nil {
		return err
	}
	t.UpdatedAt = time.Now().UTC()
	return s.save()
}

func (s *Store) SetRunID(id, runID string) error {
	return s.update(id, func(t *Task) error {
		t.RunID = runID
		return nil
	})
}

// SetOutcome records a child-run result. Terminal results are written when the
// run completes; needs_input is set when the child requested user input.
func (s *Store) SetOutcome(id string, status, text string) error {
	return s.update(id, func(t *Task) error {
		switch status {
		case StatusCompleted:
			t.Status = StatusCompleted
			t.Result = text
		case StatusNeedsInput:
			t.Status = StatusNeedsInput
			t.Question = text
		case StatusFailed:
			t.Status = StatusFailed
			t.Result = text
		default:
			return fmt.Errorf("unknown outcome %q", status)
		}
		t.Reported = false
		return nil
	})
}

func (s *Store) SetFailed(id, reason string) error {
	return s.update(id, func(t *Task) error {
		t.Status = StatusFailed
		t.Result = reason
		t.Reported = false
		return nil
	})
}

func (s *Store) Cancel(id string) error {
	return s.update(id, func(t *Task) error {
		t.Status = StatusCancelled
		t.Reported = false
		return nil
	})
}

// Reply appends a user reply and returns the task to running so the child
// agent re-runs with the new input.
func (s *Store) Reply(id, content string) error {
	return s.update(id, func(t *Task) error {
		t.Replies = append(t.Replies, Reply{Content: content, CreatedAt: time.Now().UTC()})
		t.Status = StatusRunning
		t.Question = ""
		t.Reported = false
		t.RunID = ""
		return nil
	})
}

func (s *Store) MarkReported(id string) error {
	return s.update(id, func(t *Task) error {
		t.Reported = true
		return nil
	})
}
