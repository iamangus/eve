package store

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestPrimaryCreationAndFirstMessageTitleAreAtomic(t *testing.T) {
	st, _ := New(t.TempDir())
	ids := make(chan string, 8)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			id, err := st.EnsurePrimaryConversation()
			if err != nil {
				t.Error(err)
			}
			ids <- id
		}()
	}
	workers.Wait()
	close(ids)
	primary := st.PrimaryConversationID()
	for id := range ids {
		if id != primary {
			t.Fatal("multiple primary conversations created")
		}
	}
	for _, text := range []string{strings.Repeat("😀", 50), strings.Repeat("é", 50)} {
		workers.Add(1)
		go func(text string) {
			defer workers.Done()
			if _, _, err := st.AppendQueuedOwnerMessageWithSource(primary, text, "web", "owner", "", "", text); err != nil {
				t.Error(err)
			}
		}(text)
	}
	workers.Wait()
	conv, _ := st.GetConversation(primary)
	if !utf8.ValidString(conv.Title) || conv.Title != string([]rune(conv.Messages[0].Content)[:40])+"…" {
		t.Fatal("first user message did not own a valid Unicode title")
	}
}
