package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"html"
	"io"
	"math"
	"mime"
	"net"
	stdmail "net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-msgauth/authres"

	"github.com/iamangus/eve/internal/store"
)

const (
	maxBodyBytes      = 512 * 1024
	initialWindowDays = 7
)

// Check verifies that the account credentials work by connecting and
// authenticating to the IMAP server.
func Check(a store.Account) error {
	return CheckContext(context.Background(), a)
}

func CheckContext(ctx context.Context, a store.Account) error {
	c, stop, err := dialContext(ctx, a)
	if err != nil {
		return err
	}
	defer stop()

	if err := c.Login(a.Username, a.Password).Wait(); err != nil {
		return fmt.Errorf("imap login: %w", err)
	}
	return nil
}

// FetchNew connects to the account's inbox and returns all messages with a UID
// greater than the account's cursor, along with the highest UID seen. The
// first fetch (cursor 0) is limited to the last initialWindowDays.
func FetchNew(a store.Account) ([]store.EmailMessage, uint32, error) {
	return fetchNew(context.Background(), a, new(uint32))
}

func fetchNew(ctx context.Context, a store.Account, validity *uint32) ([]store.EmailMessage, uint32, error) {
	*validity = a.UIDValidity

	c, stop, err := dialContext(ctx, a)
	if err != nil {
		return nil, a.LastUID, err
	}
	defer stop()

	if err := c.Login(a.Username, a.Password).Wait(); err != nil {
		return nil, a.LastUID, fmt.Errorf("imap login: %w", err)
	}
	selected, err := c.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, a.LastUID, fmt.Errorf("select inbox: %w", err)
	}
	*validity = selected.UIDValidity
	if a.UIDValidity != 0 && a.UIDValidity != selected.UIDValidity {
		a.LastUID = 0
	}
	if a.LastUID == math.MaxUint32 {
		return nil, a.LastUID, nil
	}

	uidSet := new(imap.UIDSet)
	uidSet.AddRange(imap.UID(a.LastUID+1), 0)
	crit := &imap.SearchCriteria{UID: []imap.UIDSet{*uidSet}}
	if a.LastUID == 0 {
		crit.Since = time.Now().AddDate(0, 0, -initialWindowDays)
	}
	res, err := c.UIDSearch(crit, nil).Wait()
	if err != nil {
		return nil, a.LastUID, fmt.Errorf("imap search: %w", err)
	}
	uids := res.AllUIDs()
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	if len(uids) > 100 {
		uids = uids[:100]
	}
	if len(uids) == 0 {
		return nil, a.LastUID, nil
	}

	// The search result is authoritative for the cursor: its UIDs are the
	// exact messages that matched, independent of how (or whether) the per-
	// message UID is populated in the FETCH response.
	var maxUID uint32
	for _, u := range uids {
		if uint32(u) > maxUID {
			maxUID = uint32(u)
		}
	}

	fetchSet := imap.UIDSet{}
	for _, u := range uids {
		fetchSet.AddNum(u)
	}
	bufs, err := c.Fetch(fetchSet, &imap.FetchOptions{
		UID:         true,
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{{Peek: true, Partial: &imap.SectionPartial{Size: 2 << 20}}},
	}).Collect()
	if err != nil {
		return nil, a.LastUID, fmt.Errorf("imap fetch: %w", err)
	}

	var msgs []store.EmailMessage
	for _, b := range bufs {
		if b.UID == 0 || b.Envelope == nil {
			return nil, a.LastUID, fmt.Errorf("IMAP returned a message without UID or envelope; cursor retained")
		}
		if m := buildMessage(b); m != nil {
			for _, section := range b.BodySection {
				m.AuthenticatedSender = authenticatedSender(section.Bytes, a.TrustedAuthservID, m.From)
			}
			msgs = append(msgs, *m)
		}
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].UID < msgs[j].UID })
	return msgs, maxUID, nil
}

func dial(a store.Account) (*imapclient.Client, error) {
	c, _, err := dialContext(context.Background(), a)
	return c, err
}

func dialContext(ctx context.Context, a store.Account) (*imapclient.Client, func(), error) {
	host := a.Host
	if a.Port != 0 {
		host = net.JoinHostPort(a.Host, strconv.Itoa(a.Port))
	}
	conn, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, nil, err
	}
	deadline := time.Now().Add(60 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	after := context.AfterFunc(ctx, func() { conn.Close() })
	stop := func() { after(); conn.Close() }
	tlsCfg := &tls.Config{ServerName: a.Host, MinVersion: tls.VersionTLS12}
	if a.UseTLS {
		tconn := tls.Client(conn, tlsCfg)
		if err := tconn.HandshakeContext(ctx); err != nil {
			stop()
			return nil, nil, err
		}
		return imapclient.New(tconn, nil), stop, nil
	}
	c, err := imapclient.NewStartTLS(conn, &imapclient.Options{TLSConfig: tlsCfg})
	if err != nil {
		stop()
		return nil, nil, err
	}
	return c, stop, nil
}

func authenticatedSender(raw []byte, trusted, from string) bool {
	if trusted == "" {
		return false
	}
	msg, err := stdmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	addr, err := stdmail.ParseAddress(from)
	if err != nil {
		return false
	}
	_, domain, ok := strings.Cut(addr.Address, "@")
	if !ok {
		return false
	}
	server, results, err := authres.Parse(msg.Header.Get("Authentication-Results"))
	if err != nil || !strings.EqualFold(server, trusted) {
		return false
	}
	for _, result := range results {
		if dmarc, ok := result.(*authres.DMARCResult); ok && dmarc.Value == authres.ResultPass && strings.EqualFold(dmarc.From, domain) {
			return true
		}
	}
	return false
}

func buildMessage(b *imapclient.FetchMessageBuffer) *store.EmailMessage {
	if b.Envelope == nil {
		return nil
	}
	m := &store.EmailMessage{
		UID:       uint32(b.UID),
		From:      joinAddrs(b.Envelope.From),
		To:        joinAddrs(b.Envelope.To),
		Subject:   strings.TrimSpace(b.Envelope.Subject),
		Date:      b.Envelope.Date,
		MessageID: b.Envelope.MessageID,
	}
	for _, section := range b.BodySection {
		if len(section.Bytes) == 0 {
			continue
		}
		m.Body = extractText(section.Bytes)
		if len(section.Bytes) >= 2<<20 {
			m.Body += "\n[Email truncated at the 2 MiB ingestion limit.]"
		}
		if strings.TrimSpace(m.Body) != "" {
			break
		}
	}
	return m
}

func joinAddrs(addrs []imap.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if s := a.Addr(); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

// extractText parses a raw RFC 5322 message and returns its text, preferring
// text/plain parts and falling back to HTML with tags stripped.
func extractText(raw []byte) string {
	reader, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		return ""
	}
	var textParts, htmlParts []string
	remaining := maxBodyBytes
	for parts := 0; parts < 100 && remaining > 0; parts++ {
		p, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if p.Header == nil {
			continue
		}
		if disposition, _, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition")); disposition == "attachment" {
			continue
		}
		ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		ct = strings.ToLower(ct)
		body, _ := io.ReadAll(io.LimitReader(p.Body, int64(remaining)))
		remaining -= len(body)
		switch {
		case strings.HasPrefix(ct, "text/plain"):
			textParts = append(textParts, string(body))
		case strings.HasPrefix(ct, "text/html"):
			htmlParts = append(htmlParts, stripHTML(string(body)))
		}
	}
	text := strings.Join(textParts, "\n\n")
	if strings.TrimSpace(text) == "" {
		text = strings.Join(htmlParts, "\n\n")
	}
	return strings.TrimSpace(text)
}

var htmlTagRE = regexp.MustCompile(`(?s)<[^>]*>`)

func stripHTML(s string) string {
	s = htmlTagRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(html.UnescapeString(s))
}
