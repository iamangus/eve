package io

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/iamangus/eve/internal/store"

	"maunium.net/go/mautrix/id"
)

// MatrixConfig configures the matrix channel. Empty Homeserver disables it.
type MatrixConfig struct {
	Homeserver  string // e.g. https://matrix.example.com
	AccessToken string
	UserID      string // the bot account, e.g. @eve:example.com
	// JoinRooms are room IDs to join at startup (best effort).
	JoinRooms []string
}

func (c MatrixConfig) Enabled() bool {
	return c.Homeserver != "" && c.AccessToken != ""
}

// matrixAdapter delivers canonical outbound messages to a matrix room.
// Recipient may be a room ID, a matrix user ID, or an identity name that
// resolves through the registry. ThreadRef carries the room id. Sending goes
// through the mautrix client so messages in E2EE rooms are encrypted; rooms
// without encryption are sent in plaintext as before.
type matrixAdapter struct {
	cfg   MatrixConfig
	reg   *Registry
	store *store.Store
	hub   *Hub
	e2ee  *MatrixE2EE
}

func (a *matrixAdapter) Type() ChannelType { return ChannelMatrix }

func (a *matrixAdapter) Send(ctx context.Context, msg OutboundMessage) error {
	roomID := msg.ThreadRef
	if roomID == "" {
		roomID = msg.Recipient
	}
	if roomID == "" {
		return fmt.Errorf("matrix: no room id")
	}
	// Resolve an identity name (not a room or @user) through the registry.
	if !strings.HasPrefix(roomID, "!") && !strings.HasPrefix(roomID, "@") {
		if snap, ok := a.reg.Lookup("matrix", roomID); ok {
			roomID = snap.DefaultRecipient
		}
		if !strings.HasPrefix(roomID, "!") && !strings.HasPrefix(roomID, "@") {
			return fmt.Errorf("matrix: unresolved recipient %q", msg.Recipient)
		}
	}
	if err := a.sendText(ctx, roomID, msg.Text); err != nil {
		return err
	}
	// Record the delivery in the conversation so the web UI shows it as a
	// message Eve sent via matrix, tagged with the matrix channel.
	m, err := a.store.AppendAssistantMessageReturn(msg.ConversationID, "", msg.Text, "matrix", "eve")
	if err != nil {
		return err
	}
	a.hub.Broadcast(Event{
		Type:   EventMessage,
		ConvID: msg.ConversationID,
		Data:   m,
	})
	return nil
}

// sendText delivers a message through the mautrix client so encrypted rooms
// get encrypted messages. If the crypto client is unavailable (should not
// happen once enabled), it falls back to plaintext via the client-server API.
func (a *matrixAdapter) sendText(ctx context.Context, roomID, body string) error {
	if a.e2ee != nil {
		_, err := a.e2ee.Client().SendText(ctx, id.RoomID(roomID), body)
		if err != nil {
			return fmt.Errorf("matrix: send: %w", err)
		}
		return nil
	}
	return a.cfg.SendRoom(ctx, roomID, body)
}

// SendRoom delivers a plain-text message to a matrix room using the client
// server API's PUT /rooms/{roomId}/send/m.room.message.
func (c MatrixConfig) SendRoom(ctx context.Context, roomID, body string) error {
	endpoint := strings.TrimSuffix(c.Homeserver, "/") +
		"/_matrix/client/v3/rooms/" + roomIDPath(roomID) + "/send/m.room.message"
	content := map[string]any{
		"msgtype": "m.text",
		"body":    body,
	}
	payload, err := json.Marshal(content)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("matrix: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var er struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&er)
		return fmt.Errorf("matrix: send %s: %s", resp.Status, er.Error)
	}
	return nil
}

// roomIDPath URL-escapes each path segment of a room id. Room ids commonly
// contain a colon and an exclamation mark, neither of which needs escaping
// in a path segment, but we stay safe.
func roomIDPath(roomID string) string {
	segs := strings.Split(roomID, "/")
	for i := range segs {
		segs[i] = pathEscape(segs[i])
	}
	return strings.Join(segs, "/")
}

func pathEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '~', r == '!', r == ':',
			r == '(', r == ')', r == '*', r == '\'', r == '+', r == ',':
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "%%%02X", r)
		}
	}
	return b.String()
}
