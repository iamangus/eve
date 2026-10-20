package io

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/iamangus/eve/internal/store"
)

// SMTPConfig configures outbound email delivery. Empty host disables the
// email channel entirely.
type SMTPConfig struct {
	Host      string
	Port      int
	Username  string
	Password  string
	From      string
	Recipient string
	tlsConfig *tls.Config
}

var ErrSMTPAmbiguous = errors.New("SMTP delivery acknowledgement unknown; retry may duplicate mail")

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
	ident *Resolver
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
		if a.ident == nil {
			return fmt.Errorf("email: unresolved recipient %q", msg.Recipient)
		}
		var err error
		to, err = a.ident.Destination(to, ChannelEmail)
		if err != nil {
			return err
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
	fromAddr, err := mail.ParseAddress(c.From)
	if err != nil {
		return fmt.Errorf("email: invalid sender: %w", err)
	}
	toAddr, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("email: invalid recipient: %w", err)
	}
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	var auth smtp.Auth
	if c.Username != "" || c.Password != "" {
		auth = smtp.PlainAuth("", c.Username, c.Password, c.Host)
	}
	headers := []string{
		"From: " + fromAddr.String(),
		"To: " + toAddr.String(),
		"Subject: Eve",
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: 8bit",
	}
	if len(turnIDs) > 0 && turnIDs[0] != "" {
		headers = append(headers, "Message-ID: "+stableMessageID(turnIDs[0], to, c.From))
	}
	msg := strings.Join(append(headers, "", body), "\r\n")
	var conn *smtp.Client
	nconn, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("email: dial: %w", err)
	}
	defer nconn.Close()
	deadline := time.Now().Add(60 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = nconn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { nconn.Close() })
	defer stop()
	if c.Port == 465 {
		tconn := tls.Client(nconn, c.transportTLS())
		if err := tconn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("email: TLS: %w", err)
		}
		conn, err = smtp.NewClient(tconn, c.Host)
	} else {
		conn, err = smtp.NewClient(nconn, c.Host)
	}
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", addr, err)
	}
	defer conn.Close()
	if c.Port != 465 {
		if err := conn.StartTLS(c.transportTLS()); err != nil {
			return fmt.Errorf("email: starttls: %w", err)
		}
	}
	if auth != nil {
		if err := conn.Auth(auth); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}
	if err := conn.Mail(fromAddr.Address); err != nil {
		return fmt.Errorf("email: mail from: %w", err)
	}
	if err := conn.Rcpt(toAddr.Address); err != nil {
		return fmt.Errorf("email: rcpt %s: %w", to, err)
	}
	w, err := conn.Data()
	if err != nil {
		return fmt.Errorf("email: data: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("%w: write: %v", ErrSMTPAmbiguous, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("%w: close: %v", ErrSMTPAmbiguous, err)
	}
	_ = conn.Quit()
	return nil
}

func (c SMTPConfig) transportTLS() *tls.Config {
	if c.tlsConfig != nil {
		return c.tlsConfig.Clone()
	}
	return &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
}

func stableMessageID(turnID, to, from string) string {
	sum := sha256.Sum256([]byte(turnID + "\x00" + to))
	domain := "eve.local"
	if _, addressDomain, ok := strings.Cut(from, "@"); ok {
		domain = strings.TrimSuffix(strings.TrimSpace(addressDomain), ">")
	}
	return "<eve-" + hex.EncodeToString(sum[:16]) + "@" + domain + ">"
}
