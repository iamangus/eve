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

	"github.com/iamangus/eve/internal/durable"
)

// Task statuses. A task runs as a child agent in agentfoundry; the poller
// advances it to a terminal state or to needs_input (the child requested
// information from the user via structured output).
const (
	StatusRunning    = "running"
	StatusNeedsInput = "needs_input"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"
)

var ErrNotFound = errors.New("task not found")
var ErrConflict = errors.New("task state changed")

// Task is a background subtask Eve spawned. It is a mini-conversation: the
// child agent may reply multiple times, and the user may reply back through
// reply_task, which re-runs the child with the new input.
type Task struct {
	ID                  string    `json:"id"`
	ConversationID      string    `json:"conversation_id"`
	AgentID             string    `json:"agent_id"`
	AgentName           string    `json:"agent_name"`
	Message             string    `json:"message"`
	Replies             []Reply   `json:"replies,omitempty"`
	Status              string    `json:"status"`
	RunID               string    `json:"run_id,omitempty"`
	Attempt             int       `json:"attempt,omitempty"`
	Revision            uint64    `json:"revision,omitempty"`
	Result              string    `json:"result,omitempty"`
	Question            string    `json:"question,omitempty"`
	Reported            bool      `json:"reported,omitempty"`
	CancellationPending bool      `json:"cancellation_pending,omitempty"`
	Archived            bool      `json:"archived,omitempty"`
	DispatchFailures    int       `json:"dispatch_failures,omitempty"`
	DispatchError       string    `json:"dispatch_error,omitempty"`
	NextDispatchAt      time.Time `json:"next_dispatch_at,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
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
	if err := os.MkdirAll(dir, 0o700); err != nil {
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
	return durable.JSON(filepath.Join(s.dir, "tasks.json"), s.tasks)
}

func cloneTask(t Task) Task {
	t.Replies = append([]Reply(nil), t.Replies...)
	return t
}

func (t Task) DispatchKey() string {
	if t.Attempt == 0 {
		return t.ID
	}
	return fmt.Sprintf("%s-attempt-%d", t.ID, t.Attempt)
}

func (s *Store) Create(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tasks[t.ID] != nil {
		return ErrConflict
	}
	copy := cloneTask(*t)
	s.tasks[t.ID] = &copy
	if err := s.save(); err != nil {
		delete(s.tasks, t.ID)
		return err
	}
	return nil
}

func (s *Store) Get(id string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok {
		return s.archived(id)
	}
	return cloneTask(*t), nil
}

func (s *Store) List() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, cloneTask(*t))
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
			out = append(out, cloneTask(*t))
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
		if t.Status == StatusRunning {
			out = append(out, cloneTask(*t))
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
			out = append(out, cloneTask(*t))
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
	next := cloneTask(*t)
	if err := fn(&next); err != nil {
		return err
	}
	next.UpdatedAt = time.Now().UTC()
	next.Revision++
	s.tasks[id] = &next
	if err := s.save(); err != nil {
		s.tasks[id] = t
		return err
	}
	return nil
}

func (s *Store) AttachRun(expected Task, runID string) error {
	return s.update(expected.ID, func(t *Task) error {
		if t.Status != StatusRunning || t.Attempt != expected.Attempt || t.RunID != expected.RunID {
			return ErrConflict
		}
		t.RunID = runID
		t.DispatchError = ""
		t.DispatchFailures = 0
		t.NextDispatchAt = time.Time{}
		return nil
	})
}

func (s *Store) DispatchFailed(expected Task, reason string) error {
	return s.update(expected.ID, func(t *Task) error {
		if t.Status != StatusRunning || t.Attempt != expected.Attempt || t.RunID != "" {
			return ErrConflict
		}
		t.DispatchFailures++
		t.DispatchError = reason
		t.NextDispatchAt = time.Now().Add(time.Second << min(t.DispatchFailures, 8))
		return nil
	})
}

func (s *Store) TrackCancelledAttempt(expected Task, runID string) error {
	return s.update(expected.ID, func(t *Task) error {
		if t.Status != StatusCancelled || t.Attempt != expected.Attempt || (t.RunID != "" && t.RunID != runID) {
			return ErrConflict
		}
		t.RunID = runID
		t.CancellationPending = true
		return nil
	})
}

func (s *Store) Finish(expected Task, status, text string) error {
	return s.update(expected.ID, func(t *Task) error {
		if t.Status != StatusRunning || t.Attempt != expected.Attempt || t.RunID != expected.RunID {
			return ErrConflict
		}
		switch status {
		case StatusCompleted, StatusFailed:
			t.Result = text
		case StatusNeedsInput:
			t.Question = text
		default:
			return fmt.Errorf("unknown outcome %q", status)
		}
		t.Status = status
		t.Reported = false
		return nil
	})
}

func (s *Store) Report(expected Task) error {
	return s.update(expected.ID, func(t *Task) error {
		if t.Revision != expected.Revision {
			return ErrConflict
		}
		t.Reported = true
		return nil
	})
}

func (s *Store) Cancel(id string) error {
	return s.update(id, func(t *Task) error {
		if t.Terminal() {
			return ErrConflict
		}
		t.CancellationPending = t.Status == StatusRunning && t.RunID != ""
		t.Status = StatusCancelled
		t.Reported = false
		return nil
	})
}

func (s *Store) CancelAcknowledged(id, runID string) error {
	return s.update(id, func(t *Task) error {
		if t.Status != StatusCancelled || t.RunID != runID {
			return ErrConflict
		}
		t.CancellationPending = false
		return nil
	})
}

// Reply appends a user reply and returns the task to running so the child
// agent re-runs with the new input.
func (s *Store) Reply(id, content string) error {
	return s.update(id, func(t *Task) error {
		if t.Status != StatusNeedsInput {
			return ErrConflict
		}
		t.Attempt++
		t.DispatchFailures = 0
		t.DispatchError = ""
		t.NextDispatchAt = time.Time{}
		t.Replies = append(t.Replies, Reply{Content: content, CreatedAt: time.Now().UTC()})
		t.Status = StatusRunning
		t.Question = ""
		t.Reported = false
		t.RunID = ""
		return nil
	})
}
