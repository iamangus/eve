package io

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/iamangus/eve/internal/store"
)

// SMTPConfig configures outbound email delivery. Empty host disables the
// email channel entirely.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

func (c SMTPConfig) Enabled() bool {
	return c.Host != ""
}

// emailAdapter delivers canonical outbound messages via SMTP. Recipient may
// be an identity name or a raw address; raw addresses pass through, identity
// names resolve through the registry.
type emailAdapter struct {
	cfg   SMTPConfig
	reg   *Registry
	store *store.Store
	hub   *Hub
}

func (a *emailAdapter) Type() ChannelType { return ChannelEmail }

func (a *emailAdapter) Send(ctx context.Context, msg OutboundMessage) error {
	if a.store.HasAssistantForTurn(msg.ConversationID, msg.TurnID) {
		return nil
	}
	to := msg.Recipient
	if to == "" {
		return fmt.Errorf("email: no recipient")
	}
	if !strings.Contains(to, "@") {
		if snap, ok := a.reg.Lookup("email", to); ok {
			to = snap.DefaultRecipient
		}
		if !strings.Contains(to, "@") {
			return fmt.Errorf("email: unresolved recipient %q", msg.Recipient)
		}
	}
	if err := a.cfg.Send(ctx, to, msg.Text, msg.TurnID); err != nil {
		return err
	}
	// Record the delivery in the conversation so the web UI shows it as a
	// message Eve sent via email, tagged with the email channel.
	m, created, err := a.store.AppendAssistantForTurn(msg.ConversationID, "", msg.TurnID, msg.Text, "email", "eve")
	if err != nil {
		return err
	}
	if !created {
		return nil
	}
	a.hub.Broadcast(Event{
		Type:   EventMessage,
		ConvID: msg.ConversationID,
		Data:   m,
	})
	return nil
}

// Send delivers a plain-text email to one recipient through the configured
// SMTP server.
func (c SMTPConfig) Send(ctx context.Context, to, body string, turnIDs ...string) error {
	addr := fmt.Sprintf("%s:%d", c.Host, c.Port)
	var auth smtp.Auth
	if c.Username != "" || c.Password != "" {
		auth = smtp.PlainAuth("", c.Username, c.Password, c.Host)
	}
	headers := []string{
		"From: " + c.From,
		"To: " + to,
		"Subject: Eve",
	}
	if len(turnIDs) > 0 && turnIDs[0] != "" {
		headers = append(headers, "Message-ID: "+stableMessageID(turnIDs[0], to, c.From))
	}
	msg := strings.Join(append(headers, "", body), "\r\n")
	var conn *smtp.Client
	var err error
	if c.Port == 465 {
		tconn, terr := tls.Dial("tcp", addr, &tls.Config{ServerName: c.Host})
		if terr != nil {
			return fmt.Errorf("email: tls dial %s: %w", addr, terr)
		}
		conn, err = smtp.NewClient(tconn, c.Host)
	} else {
		conn, err = smtp.Dial(addr)
	}
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", addr, err)
	}
	defer conn.Close()
	if c.Port != 465 {
		if err := conn.StartTLS(&tls.Config{ServerName: c.Host}); err != nil {
			return fmt.Errorf("email: starttls: %w", err)
		}
	}
	if auth != nil {
		if err := conn.Auth(auth); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}
	if err := conn.Mail(c.From); err != nil {
		return fmt.Errorf("email: mail from: %w", err)
	}
	if err := conn.Rcpt(to); err != nil {
		return fmt.Errorf("email: rcpt %s: %w", to, err)
	}
	w, err := conn.Data()
	if err != nil {
		return fmt.Errorf("email: data: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("email: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: close: %w", err)
	}
	if err := conn.Quit(); err != nil {
		return fmt.Errorf("email: quit: %w", err)
	}
	return nil
}

func stableMessageID(turnID, to, from string) string {
	sum := sha256.Sum256([]byte(turnID + "\x00" + to))
	domain := "eve.local"
	if _, addressDomain, ok := strings.Cut(from, "@"); ok {
		domain = strings.TrimSuffix(strings.TrimSpace(addressDomain), ">")
	}
	return "<eve-" + hex.EncodeToString(sum[:16]) + "@" + domain + ">"
}
