package events

import (
	"encoding/json"
	"testing"
	"time"
)

func TestQueueLeasesRetriesAndDeduplicates(t *testing.T) {
	q, err := NewQueue(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"value":"one"}`)
	queued, err := q.Enqueue("opendev", "backend", "evt-1", payload)
	if err != nil || !queued {
		t.Fatalf("Enqueue = %t, %v", queued, err)
	}
	queued, err = q.Enqueue("opendev", "backend", "evt-1", payload)
	if err != nil || queued {
		t.Fatalf("duplicate Enqueue = %t, %v", queued, err)
	}

	now := time.Now().UTC()
	first, err := q.Lease(now, time.Minute)
	if err != nil || first == nil || first.Attempt != 1 || first.State != StateLeased {
		t.Fatalf("first Lease = %#v, %v", first, err)
	}
	if err := q.Retry(first); err != nil {
		t.Fatal(err)
	}
	second, err := q.Lease(now.Add(time.Second), time.Minute)
	if err != nil || second == nil || second.Attempt != 2 {
		t.Fatalf("second Lease = %#v, %v", second, err)
	}
	if err := q.Complete(second); err != nil {
		t.Fatal(err)
	}
	completed, err := q.Lease(now.Add(2*time.Minute), time.Minute)
	if err != nil || completed != nil {
		t.Fatalf("completed Lease = %#v, %v", completed, err)
	}
}

func TestQueueRecoversExpiredLeaseAfterRestart(t *testing.T) {
	dir := t.TempDir()
	q, err := NewQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue("opendev", "backend", "evt-1", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	leased, err := q.Lease(time.Now().UTC(), time.Millisecond)
	if err != nil || leased == nil {
		t.Fatalf("Lease = %#v, %v", leased, err)
	}
	time.Sleep(2 * time.Millisecond)
	restarted, err := NewQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := restarted.Lease(time.Now().UTC(), time.Minute)
	if err != nil || recovered == nil || recovered.ID != "evt-1" || recovered.Attempt != 2 {
		t.Fatalf("recovered Lease = %#v, %v", recovered, err)
	}
}
