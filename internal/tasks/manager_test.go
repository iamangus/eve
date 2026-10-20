package tasks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/iamangus/eve/internal/agentfoundry"
)

func TestReplyCreatesNewAttemptAndOutcomeIsAppliedOnce(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]string{"status": "completed", "response": "{\"status\":\"needs_input\",\"text\":\"question\"}"})
			return
		}
		var body struct {
			TaskID string `json:"task_id"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		keys = append(keys, body.TaskID)
		json.NewEncoder(w).Encode(map[string]string{"run_id": "run-" + body.TaskID})
	}))
	defer server.Close()
	client, _ := agentfoundry.NewClient(server.URL, "key")
	m := NewManager(st, client, Config{PollInterval: time.Second})
	task, err := m.SpawnTask(context.Background(), "conv", "agent", "agent", "work")
	if err != nil {
		t.Fatal(err)
	}
	m.poll(context.Background())
	waiting, _ := st.Get(task.ID)
	if waiting.Status != StatusNeedsInput || !waiting.Reported {
		t.Fatalf("unexpected task: %+v", waiting)
	}
	m.poll(context.Background())
	again, _ := st.Get(task.ID)
	if again.Revision != waiting.Revision {
		t.Fatal("waiting task was polled again")
	}
	if err := m.Reply(context.Background(), task.ID, "answer"); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] == keys[1] {
		t.Fatalf("dispatch keys were reused: %v", keys)
	}
	if err := st.Finish(waiting, StatusCompleted, "stale result"); err != ErrConflict {
		t.Fatalf("stale outcome accepted: %v", err)
	}
	if err := m.Cancel(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	m.poll(context.Background())
	final, _ := st.Get(task.ID)
	if final.Status != StatusCancelled {
		t.Fatalf("cancellation overwritten: %+v", final)
	}
}

func TestTaskWritesRollbackAndDoNotAliasCallers(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	task := Task{ID: "t1", Status: StatusNeedsInput, Replies: []Reply{{Content: "original"}}}
	if err := st.Create(&task); err != nil {
		t.Fatal(err)
	}
	task.Replies[0].Content = "mutated"
	copy, _ := st.Get(task.ID)
	if copy.Replies[0].Content != "original" {
		t.Fatal("store aliases caller")
	}
	if err := os.Mkdir(filepath.Join(dir, "tasks.json.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.Reply(task.ID, "answer"); err == nil {
		t.Fatal("expected write failure")
	}
	copy, _ = st.Get(task.ID)
	if copy.Status != StatusNeedsInput || len(copy.Replies) != 1 || copy.Attempt != 0 {
		t.Fatalf("failed write leaked: %+v", copy)
	}
}

func TestCancellationTracksAnAttemptThatWasDispatchedConcurrently(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	task := Task{ID: "task", Status: StatusRunning}
	if err := st.Create(&task); err != nil {
		t.Fatal(err)
	}
	if err := st.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.AttachRun(task, "run"); err != ErrConflict {
		t.Fatal("late dispatch overwrote cancellation")
	}
	if err := st.TrackCancelledAttempt(task, "run"); err != nil {
		t.Fatal(err)
	}
	st, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := st.Get(task.ID)
	if !restored.CancellationPending || restored.RunID != "run" || restored.Status != StatusCancelled {
		t.Fatal("late dispatch cancellation was not durable")
	}
}

func TestTransientDispatchFailureKeepsOneDurableTaskAndStableAttempt(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	var mu sync.Mutex
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			TaskID string `json:"task_id"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		keys = append(keys, body.TaskID)
		attempts := len(keys)
		mu.Unlock()
		if attempts == 1 {
			http.Error(w, "temporary outage", http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"run_id": "recovered"})
	}))
	defer server.Close()
	client, _ := agentfoundry.NewClient(server.URL, "key")
	m := NewManager(st, client, Config{})
	task, err := m.SpawnTask(context.Background(), "conv", "agent", "agent", "work")
	if err != nil || task.DispatchError == "" || task.RunID != "" {
		t.Fatalf("task not durably queued: %+v %v", task, err)
	}
	st, _ = NewStore(dir)
	m = NewManager(st, client, Config{})
	m.poll(context.Background())
	mu.Lock()
	count := len(keys)
	mu.Unlock()
	if count != 1 {
		t.Fatal("persisted dispatch backoff ignored")
	}
	if err := st.update(task.ID, func(task *Task) error { task.NextDispatchAt = time.Time{}; return nil }); err != nil {
		t.Fatal(err)
	}
	m.poll(context.Background())
	recovered, _ := st.Get(task.ID)
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 2 || keys[0] != keys[1] || recovered.RunID != "recovered" {
		t.Fatal("dispatch retry lost stable task identity")
	}
}
