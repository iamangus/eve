package events

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	second, err := q.Lease(now.Add(2*time.Second), time.Minute)
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

func TestFailedAgentInputGetsNewIDWithoutChangingUncertainDeliveryID(t *testing.T) {
	dir := t.TempDir()
	q, err := NewQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue("opendev", "backend", "evt-1", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	first, err := q.Lease(time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Retry(first); err != nil {
		t.Fatal(err)
	}
	lease, err := q.Lease(time.Now().Add(time.Hour), time.Minute)
	if err != nil || lease.InputID != first.InputID {
		t.Fatalf("uncertain acceptance changed input ID: %+v %v", lease, err)
	}
	for failure := 1; failure <= 5; failure++ {
		oldID := lease.InputID
		if err := q.AgentInputFailed(lease, "worker failed"); err != nil {
			t.Fatal(err)
		}
		q, err = NewQueue(dir)
		if err != nil {
			t.Fatal(err)
		}
		lease, err = q.Lease(time.Now().Add(time.Hour), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if failure == 5 {
			if lease != nil || q.events[eventKey("opendev", "evt-1")].State != StateFailed || q.events[eventKey("opendev", "evt-1")].LastError != "worker failed" {
				t.Fatalf("exhausted event not preserved as failed: %+v", lease)
			}
			break
		}
		if lease == nil || lease.InputID == oldID || lease.AgentFailures != failure {
			t.Fatalf("confirmed failure reused input ID: %+v", lease)
		}
	}
}

func TestFailedEnqueueCanBeRetriedWithoutLosingEvent(t *testing.T) {
	dir := t.TempDir()
	q, err := NewQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "internal_events.json.tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if created, err := q.Enqueue("opendev", "backend", "event-1", json.RawMessage(`{}`)); created || err == nil {
		t.Fatalf("event acknowledged without persistence: %t %v", created, err)
	}
	if err := os.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	if created, err := q.Enqueue("opendev", "backend", "event-1", json.RawMessage(`{}`)); !created || err != nil {
		t.Fatalf("retry was deduplicated in memory: %t %v", created, err)
	}
	q, err = NewQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if event, err := q.Lease(time.Now(), time.Minute); err != nil || event == nil || event.ID != "event-1" {
		t.Fatalf("retried event did not survive restart: %+v %v", event, err)
	}
}

func TestFailedLeaseAndCompletionKeepDurableQueueState(t *testing.T) {
	dir := t.TempDir()
	q, err := NewQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue("opendev", "backend", "event-1", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "internal_events.json.tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if event, err := q.Lease(time.Now(), time.Minute); err == nil || event != nil {
		t.Fatalf("lease succeeded without persistence: %+v %v", event, err)
	}
	if pending := q.events[eventKey("opendev", "event-1")]; pending.State != StatePending || pending.Attempt != 0 {
		t.Fatalf("failed lease mutated memory: %+v", pending)
	}
	if err := os.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	lease, err := q.Lease(time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(lease); err == nil {
		t.Fatal("completion succeeded without persistence")
	}
	if leased := q.events[eventKey("opendev", "event-1")]; leased.State != StateLeased || leased.LeaseToken != lease.LeaseToken {
		t.Fatalf("failed completion lost lease: %+v", leased)
	}
	if err := os.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(lease); err != nil {
		t.Fatal(err)
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

func TestRetryBackoffLetsNewEventProceed(t *testing.T) {
	q, err := NewQueue(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"slow", "ready"} {
		if _, err := q.Enqueue("opendev", "backend", id, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	first, err := q.Lease(now, time.Minute)
	if err != nil || first.ID != "slow" {
		t.Fatalf("first lease: %+v %v", first, err)
	}
	if err := q.Retry(first); err != nil {
		t.Fatal(err)
	}
	second, err := q.Lease(time.Now().UTC(), time.Minute)
	if err != nil || second.ID != "ready" {
		t.Fatalf("retry starved ready event: %+v %v", second, err)
	}
}
