package io

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMatrixSendUsesStableTransactionPath(t *testing.T) {
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method: %s", r.Method)
		}
		paths = append(paths, r.URL.EscapedPath())
		w.Write([]byte(`{"event_id":"$reply"}`))
	}))
	defer server.Close()
	config := MatrixConfig{Homeserver: server.URL, AccessToken: "token"}
	for range 2 {
		if err := config.SendRoom(context.Background(), "!room:example.org", "reply", "chat-conv-2"); err != nil {
			t.Fatal(err)
		}
	}
	if len(paths) != 2 || paths[0] != paths[1] || !strings.HasSuffix(paths[0], "/send/m.room.message/chat-conv-2") {
		t.Fatalf("matrix idempotency paths: %v", paths)
	}
}
