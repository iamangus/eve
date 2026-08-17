package agentfoundry

import (
	"context"
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
