package tasks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveRetainsResultsAndDoesNotArchiveActiveWork(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.Create(&Task{ID: "finished", Status: StatusCompleted, Reported: true, Result: "result"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Create(&Task{ID: "active", Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := st.Archive("active"); err == nil {
		t.Fatal("active work archived")
	}
	if err := st.Archive("finished"); err != nil {
		t.Fatal(err)
	}
	if len(st.List()) != 1 {
		t.Fatal("archived task remained in hot store")
	}
	st, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	task, err := st.Get("finished")
	if err != nil || !task.Archived || task.Result != "result" {
		t.Fatalf("archive not readable: %+v %v", task, err)
	}
	items, err := st.ListArchived(0, 100)
	if err != nil || len(items) != 1 {
		t.Fatal("archive listing failed")
	}
	if _, err := st.Get("../tasks"); err != ErrNotFound {
		t.Fatal("unsafe archive path accepted")
	}
}

func TestArchiveFailureLeavesTaskReadableAndRetryable(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	st.Create(&Task{ID: "finished", Status: StatusCompleted, Reported: true, Result: "keep"})
	if err := os.Mkdir(filepath.Join(dir, "tasks.json.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.Archive("finished"); err == nil {
		t.Fatal("expected failure")
	}
	if task, err := st.Get("finished"); err != nil || task.Result != "keep" {
		t.Fatal("archive failure lost result")
	}
	if err := os.Remove(filepath.Join(dir, "tasks.json.tmp")); err != nil {
		t.Fatal(err)
	}
	if err := st.Archive("finished"); err != nil {
		t.Fatal(err)
	}
}
