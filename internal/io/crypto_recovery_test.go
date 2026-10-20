package io

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/iamangus/eve/internal/events"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestCryptoRejectsWrongKeyAndCorruptionWithoutOverwritingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crypto.json")
	if _, err := openJSONCryptoStore(path, []byte("original")); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)
	if _, err := openJSONCryptoStore(path, []byte("rotated-token")); err == nil {
		t.Fatal("wrong pickle key accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(original, after) {
		t.Fatal("failed restore overwrote crypto identity")
	}
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openJSONCryptoStore(path, []byte("original")); err == nil {
		t.Fatal("missing account silently regenerated")
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	json.Unmarshal(original, &snapshot)
	snapshot["message_indices"] = map[string]any{"invalid-key": map[string]any{"event_id": "event"}}
	corrupt, _ := json.Marshal(snapshot)
	os.WriteFile(path, corrupt, 0o600)
	if _, err := openJSONCryptoStore(path, []byte("original")); err == nil {
		t.Fatal("invalid replay index silently skipped")
	}
}

func TestConcurrentCryptoMetadataAndReplayWritesSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crypto.json")
	st, err := openJSONCryptoStore(path, []byte("key"))
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			if _, err := st.ms.ValidateMessageIndex(context.Background(), "session", id.EventID(string(rune('a'+index))), uint(index), int64(index)); err != nil {
				t.Error(err)
			}
			if err := st.SaveFilterID(context.Background(), "@eve:test", "filter"); err != nil {
				t.Error(err)
			}
		}(i)
	}
	workers.Wait()
	st, err = openJSONCryptoStore(path, []byte("key"))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.ms.MessageIndices) != 8 {
		t.Fatal("concurrent replay writes lost")
	}
}

func TestMatrixSyncRedeliveryIgnoresVolatileUnsignedMetadata(t *testing.T) {
	queue, _ := events.NewQueue(t.TempDir())
	poller := &MatrixPoller{cfg: MatrixConfig{UserID: "@eve:test"}, manager: &Manager{MatrixInbox: queue}}
	evt := &event.Event{ID: "$event", RoomID: "!room:test", Sender: "@owner:test", Type: event.EventMessage, Content: event.Content{Parsed: &event.MessageEventContent{MsgType: event.MsgText, Body: "hello"}}}
	poller.enqueue(evt)
	evt.Unsigned.Age = 12345
	poller.enqueue(evt)
	if poller.acceptErr != nil || len(queue.Snapshot()) != 1 {
		t.Fatalf("redelivery blocked sync: %v", poller.acceptErr)
	}
}
