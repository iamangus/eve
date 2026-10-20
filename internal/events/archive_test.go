package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompletedEventArchivePreservesDeduplicationAndStatus(t *testing.T) {
	dir := t.TempDir()
	q, _ := NewQueue(dir)
	q.Enqueue("source", "outbound", "done", json.RawMessage(`{"text":"hello"}`))
	entry, _ := q.Lease(time.Now(), time.Minute)
	if err := q.Complete(entry); err != nil {
		t.Fatal(err)
	}
	q.Enqueue("source", "outbound", "pending", json.RawMessage(`{}`))
	if count, err := q.ArchiveCompleted(time.Now().Add(time.Second)); err != nil || count != 1 {
		t.Fatalf("archive: %d %v", count, err)
	}
	q, err := NewQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Snapshot()) != 1 {
		t.Fatal("completed event still in hot store")
	}
	if event, ok := q.Status("source", "done"); !ok || event.State != StateDone {
		t.Fatal("archived status unavailable")
	}
	if created, err := q.Enqueue("source", "outbound", "done", json.RawMessage(`{"text":"hello"}`)); err != nil || created {
		t.Fatal("archival lost deduplication")
	}
	if _, err := q.Enqueue("source", "outbound", "done", json.RawMessage(`{"text":"changed"}`)); err == nil {
		t.Fatal("archived payload conflict accepted")
	}
	if count, err := q.ExpireCompleted(time.Now().Add(-time.Hour)); err != nil || count != 0 {
		t.Fatal("unexpired dedup record removed")
	}
	if count, err := q.ExpireCompleted(time.Now().Add(time.Hour)); err != nil || count != 1 {
		t.Fatal("explicit retry window did not expire completed record")
	}
	if created, err := q.Enqueue("source", "outbound", "done", json.RawMessage(`{"text":"hello"}`)); err != nil || !created {
		t.Fatal("expired identity was not reusable after the documented retry window")
	}
}

func TestArchiveFailureDoesNotLoseCompletedEvent(t *testing.T) {
	dir := t.TempDir()
	q, _ := NewQueue(dir)
	q.Enqueue("s", "outbound", "id", json.RawMessage(`{}`))
	entry, _ := q.Lease(time.Now(), time.Minute)
	q.Complete(entry)
	if err := os.Mkdir(filepath.Join(dir, "internal_events.json.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ArchiveCompleted(time.Now().Add(time.Second)); err == nil {
		t.Fatal("expected archive failure")
	}
	if len(q.Snapshot()) != 1 {
		t.Fatal("failed archive changed hot state")
	}
}
