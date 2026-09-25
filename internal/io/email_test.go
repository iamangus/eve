package io

import "testing"

func TestEmailReplyMessageIDStableAcrossRetry(t *testing.T) {
	a := stableMessageID("chat-conv-2", "owner@example.org", "eve@example.org")
	if a != stableMessageID("chat-conv-2", "owner@example.org", "eve@example.org") || a == stableMessageID("chat-conv-3", "owner@example.org", "eve@example.org") {
		t.Fatal("email reply identity was not stable per turn")
	}
}
