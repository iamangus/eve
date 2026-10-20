package store

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkConversationAppend(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			st, _ := New(b.TempDir())
			conv, _ := st.CreateConversation("benchmark")
			record := st.convs[conv.ID]
			for i := 0; i < size; i++ {
				record.Messages = append(record.Messages, Message{ID: int64(i + 1), Role: "user", Content: strings.Repeat("x", 200)})
			}
			record.NextMsgID = int64(size + 1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := st.AppendUserMessage(conv.ID, "new message", "web", "owner"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
