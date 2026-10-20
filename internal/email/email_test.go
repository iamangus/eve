package email

import (
	"strings"
	"testing"
)

func TestOwnerEmailRequiresTrustedAlignedAuthenticationResult(t *testing.T) {
	raw := []byte("From: owner@example.com\r\nAuthentication-Results: mx.example.net; dmarc=pass header.from=example.com\r\n\r\nhello")
	if !authenticatedSender(raw, "mx.example.net", "owner@example.com") {
		t.Fatal("trusted aligned result rejected")
	}
	if authenticatedSender(raw, "", "owner@example.com") {
		t.Fatal("unconfigured gateway trusted")
	}
	if authenticatedSender(raw, "other.example.net", "owner@example.com") {
		t.Fatal("wrong gateway trusted")
	}
	if authenticatedSender(raw, "mx.example.net", "owner@other.com") {
		t.Fatal("unaligned domain trusted")
	}
	if authenticatedSender([]byte(strings.ReplaceAll(string(raw), "dmarc=pass", "dmarc=fail")), "mx.example.net", "owner@example.com") {
		t.Fatal("failed DMARC trusted")
	}
}

func TestExtractTextSkipsAttachmentsAndBoundsTotal(t *testing.T) {
	raw := "MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=parts\r\n\r\n--parts\r\nContent-Type: text/plain\r\n\r\nhello\r\n--parts\r\nContent-Type: text/plain\r\nContent-Disposition: attachment; filename=secret.txt\r\n\r\nattachment\r\n--parts--\r\n"
	if got := extractText([]byte(raw)); got != "hello" {
		t.Fatalf("unexpected body: %q", got)
	}
}
