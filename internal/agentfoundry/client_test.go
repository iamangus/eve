package agentfoundry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAwaitRunTextAcceptsEmptyDoneEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: done\ndata: \n\n"))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got, err := client.AwaitRunText(context.Background(), "run-id", time.Second)
	if err != nil {
		t.Fatalf("AwaitRunText: %v", err)
	}
	if got != "" {
		t.Fatalf("AwaitRunText returned %q, want empty response", got)
	}
}

func TestPersistentRunRequests(t *testing.T) {
	var createSeen, inputSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agents/eve/runs":
			createSeen = r.Method == http.MethodPost && r.Header.Get("Authorization") == "Bearer secret"
			_ = json.NewEncoder(w).Encode(map[string]string{"run_id": "persistent-1"})
		case "/api/v1/runs/persistent-1/inputs":
			var body struct {
				Message string `json:"message"`
				InputID string `json:"input_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			inputSeen = r.Method == http.MethodPost && body.Message == "hello" && body.InputID == "event-1"
			_ = json.NewEncoder(w).Encode(map[string]string{"run_id": "execution-1"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	runID, err := client.CreatePersistentRun(context.Background(), "eve", PersistentRunOptions{})
	if err != nil || runID != "persistent-1" {
		t.Fatalf("CreatePersistentRun = %q, %v", runID, err)
	}
	executionID, err := client.SendPersistentRunInput(context.Background(), runID, "hello", "event-1")
	if err != nil || executionID != "execution-1" {
		t.Fatalf("SendPersistentRunInput = %q, %v", executionID, err)
	}
	if !createSeen || !inputSeen {
		t.Fatalf("persistent requests not sent as expected: create=%t input=%t", createSeen, inputSeen)
	}
}

func TestSendPersistentRunInputReturnsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"run not found"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SendPersistentRunInput(context.Background(), "missing", "hello", "input-1")
	if !IsNotFound(err) {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestIsStaleRunCoversConflict(t *testing.T) {
	conflict := &HTTPError{StatusCode: http.StatusConflict, Status: "409 Conflict", Body: "{\"error\":\"run is not active\",\"status\":\"failed\"}"}
	if !IsStaleRun(conflict) {
		t.Fatal("409 run-is-not-active should be treated as a stale run")
	}
	if IsStaleRun(&HTTPError{StatusCode: http.StatusConflict, Body: "different conflict"}) {
		t.Fatal("unrelated 409 should not be treated as stale")
	}
}
