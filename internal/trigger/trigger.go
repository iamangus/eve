package trigger

import (
	"fmt"
	"strings"
	"time"

	"github.com/iamangus/eve/internal/store"
)

// Matches reports whether the email satisfies all of the trigger's filters.
// Empty contains values are treated as pass-through.
func Matches(t store.Trigger, m store.EmailMessage) bool {
	for _, f := range t.Filters {
		if !filterMatches(f, m) {
			return false
		}
	}
	return true
}

func filterMatches(f store.Filter, m store.EmailMessage) bool {
	needle := strings.ToLower(strings.TrimSpace(f.Contains))
	if needle == "" {
		return true
	}
	var hay string
	switch f.Field {
	case "sender":
		hay = m.From
	case "recipient":
		hay = m.To
	case "subject":
		hay = m.Subject
	case "body":
		hay = m.Body
	default:
		return false
	}
	return strings.Contains(strings.ToLower(hay), needle)
}

// ComposePrompt builds the agent prompt from the trigger prompt plus the
// email details.
func ComposePrompt(t store.Trigger, m store.EmailMessage) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(t.Prompt))
	b.WriteString("\n\n--- Email ---\n")
	fmt.Fprintf(&b, "From: %s\n", m.From)
	fmt.Fprintf(&b, "To: %s\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\n", m.Subject)
	if !m.Date.IsZero() {
		fmt.Fprintf(&b, "Date: %s\n", m.Date.Format(time.RFC1123Z))
	}
	if m.MessageID != "" {
		fmt.Fprintf(&b, "Message-ID: %s\n", m.MessageID)
	}
	b.WriteString("\nBody:\n")
	b.WriteString(m.Body)
	return b.String()
}
