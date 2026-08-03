package email

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"math"
	"mime"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"

	"github.com/iamangus/eve/internal/store"
)

const (
	maxBodyBytes      = 512 * 1024
	initialWindowDays = 7
)

// Check verifies that the account credentials work by connecting and
// authenticating to the IMAP server.
func Check(a store.Account) error {
	c, err := dial(a)
	if err != nil {
		return err
	}
	defer c.Logout()

	if err := c.Login(a.Username, a.Password).Wait(); err != nil {
		return fmt.Errorf("imap login: %w", err)
	}
	return nil
}

// FetchNew connects to the account's inbox and returns all messages with a UID
// greater than the account's cursor, along with the highest UID seen. The
// first fetch (cursor 0) is limited to the last initialWindowDays.
func FetchNew(a store.Account) ([]store.EmailMessage, uint32, error) {
	if a.LastUID == math.MaxUint32 {
		return nil, a.LastUID, nil
	}

	c, err := dial(a)
	if err != nil {
		return nil, a.LastUID, err
	}
	defer c.Logout()

	if err := c.Login(a.Username, a.Password).Wait(); err != nil {
		return nil, a.LastUID, fmt.Errorf("imap login: %w", err)
	}
	if _, err := c.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, a.LastUID, fmt.Errorf("select inbox: %w", err)
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
		BodySection: []*imap.FetchItemBodySection{{}},
	}).Collect()
	if err != nil {
		return nil, a.LastUID, fmt.Errorf("imap fetch: %w", err)
	}

	var msgs []store.EmailMessage
	for _, b := range bufs {
		if m := buildMessage(b); m != nil {
			msgs = append(msgs, *m)
		}
	}
	return msgs, maxUID, nil
}

func dial(a store.Account) (*imapclient.Client, error) {
	host := a.Host
	if a.Port != 0 {
		host = net.JoinHostPort(a.Host, strconv.Itoa(a.Port))
	}
	if a.UseTLS {
		return imapclient.DialTLS(host, nil)
	}
	return imapclient.DialStartTLS(host, nil)
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
	for {
		p, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil || p.Header == nil {
			continue
		}
		ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		ct = strings.ToLower(ct)
		body, _ := io.ReadAll(io.LimitReader(p.Body, maxBodyBytes))
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
