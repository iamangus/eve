package trigger

import (
	"strings"
	"testing"
	"time"

	"github.com/iamangus/eve/internal/store"
)

func TestMatchesSender(t *testing.T) {
	tg := store.Trigger{Filters: []store.Filter{{Field: "sender", Contains: "vendor.com"}}}
	msg := store.EmailMessage{From: "Billing <billing@VENDOR.com>", Subject: "Invoice", Body: "due now"}
	if !Matches(tg, msg) {
		t.Fatal("expected match on sender")
	}
}

func TestMatchesAllFiltersAnded(t *testing.T) {
	tg := store.Trigger{Filters: []store.Filter{
		{Field: "sender", Contains: "billing@vendor.com"},
		{Field: "subject", Contains: "invoice"},
		{Field: "body", Contains: "due date"},
	}}
	msg := store.EmailMessage{From: "billing@vendor.com", Subject: "Your Invoice", Body: "The due date is Friday."}
	if !Matches(tg, msg) {
		t.Fatal("expected match when all filters match")
	}

	noSubject := store.EmailMessage{From: "billing@vendor.com", Subject: "Reminder", Body: "The due date is Friday."}
	if Matches(tg, noSubject) {
		t.Fatal("expected no match when one filter fails")
	}
}

func TestMatchesCaseInsensitive(t *testing.T) {
	tg := store.Trigger{Filters: []store.Filter{{Field: "subject", Contains: "INVOICE #123"}}}
	msg := store.EmailMessage{Subject: "Your invoice #123 is ready"}
	if !Matches(tg, msg) {
		t.Fatal("expected case-insensitive match")
	}
}

func TestMatchesEmptyFilters(t *testing.T) {
	tg := store.Trigger{Filters: nil}
	msg := store.EmailMessage{From: "anyone@example.com", Subject: "anything", Body: "whatever"}
	if !Matches(tg, msg) {
		t.Fatal("trigger with no filters should match everything")
	}
}

func TestMatchesEmptyContainsIsPassthrough(t *testing.T) {
	tg := store.Trigger{Filters: []store.Filter{{Field: "body", Contains: ""}}}
	if !Matches(tg, store.EmailMessage{Body: "x"}) {
		t.Fatal("empty contains value should not filter")
	}
}

func TestMatchesUnknownField(t *testing.T) {
	tg := store.Trigger{Filters: []store.Filter{{Field: "bogus", Contains: "x"}}}
	if Matches(tg, store.EmailMessage{Body: "x"}) {
		t.Fatal("unknown filter field should not match")
	}
}

func TestComposePrompt(t *testing.T) {
	tg := store.Trigger{Prompt: "Summarize this email and flag the due amount."}
	msg := store.EmailMessage{
		From:    "billing@vendor.com",
		To:      "me@example.com",
		Subject: "Invoice #123",
		Date:    time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
		Body:    "Please pay $250 by Friday.",
	}
	prompt := ComposePrompt(tg, msg)
	for _, want := range []string{"Summarize this email", "From: billing@vendor.com", "To: me@example.com", "Subject: Invoice #123", "Date: ", "Please pay $250 by Friday."} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
