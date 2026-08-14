package io

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// MatrixPoller runs the matrix sync loop through the mautrix client. The
// Olm machine's ProcessSyncResponse is wired in as a sync listener so
// to-device room keys, device lists and one-time-key counts are handled
// automatically; room message events (plaintext or encrypted) are forwarded
// to the manager's inbound path. Undecryptable events are never silently
// dropped: they trigger a key request and are logged with a reason.
type MatrixPoller struct {
	cfg     MatrixConfig
	manager *Manager
	e2ee    *MatrixE2EE
}

// NewMatrixPoller returns the sync poller for the configured matrix channel.
// e2ee must be non-nil (it owns the client and crypto machine).
func NewMatrixPoller(cfg MatrixConfig, m *Manager, e2ee *MatrixE2EE) *MatrixPoller {
	return &MatrixPoller{cfg: cfg, manager: m, e2ee: e2ee}
}

// Run blocks until ctx is cancelled, running mautrix's sync loop. The
// DefaultSyncer dispatches events to the handlers registered in setup.
// Sync errors are logged and surfaced in poller health, then the loop
// retries after a short backoff so a transient network blip does not take
// the channel down silently.
func (p *MatrixPoller) Run(ctx context.Context) {
	client := p.e2ee.Client()
	syncer, ok := client.Syncer.(mautrix.ExtensibleSyncer)
	if !ok {
		slog.Error("matrix: client syncer does not implement ExtensibleSyncer", "user", client.UserID)
		p.manager.RecordPollHealth("matrix", errors.New("syncer does not implement ExtensibleSyncer"))
		return
	}
	p.setupHandlers(syncer)
	p.seedStateStore(ctx)

	slog.Info("matrix sync started", "user", client.UserID, "device", client.DeviceID)
	const backoff = 5 * time.Second
	for {
		err := client.SyncWithContext(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("matrix sync error; retrying", "error", err, "backoff", backoff)
			p.manager.RecordPollHealth("matrix", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			continue
		}
		p.manager.RecordPollHealth("matrix", nil)
	}
}

// setupHandlers registers the crypto machine's sync/state handlers plus our
// message handlers. Order matters: the machine must see to-device and state
// events before we try to decrypt timeline events.
func (p *MatrixPoller) setupHandlers(syncer mautrix.ExtensibleSyncer) {
	client := p.e2ee.Client()
	mach := p.e2ee.Mach()
	syncer.OnSync(mach.ProcessSyncResponse)
	// StateStoreSyncHandler feeds m.room.encryption and membership state
	// events into the state store. Without it IsEncrypted() stays false and
	// SendMessageEvent sends plaintext into encrypted rooms.
	syncer.OnEvent(client.StateStoreSyncHandler)
	syncer.OnEventType(event.StateMember, mach.HandleMemberEvent)
	syncer.OnEventType(event.EventEncrypted, p.handleEncrypted)
	syncer.OnEventType(event.EventMessage, p.handleMessage)
}

// seedStateStore populates the state store with the current encryption
// state and membership of every joined room. On a fresh start the initial
// sync delivers full room state, but once a sync token is persisted the
// m.room.encryption event is not redelivered on subsequent incremental
// syncs — without seeding, the client would keep sending plaintext.
func (p *MatrixPoller) seedStateStore(ctx context.Context) {
	client := p.e2ee.Client()
	joined, err := client.JoinedRooms(ctx)
	if err != nil {
		slog.Warn("matrix: seed state store: joined rooms", "error", err)
		p.manager.RecordPollHealth("matrix", err)
		return
	}
	for _, roomID := range joined.JoinedRooms {
		var enc event.EncryptionEventContent
		if err := client.StateEvent(ctx, roomID, event.StateEncryption, "", &enc); err != nil {
			// 404 means the room is not encrypted; that's expected for
			// plaintext rooms and not an error worth surfacing.
			continue
		}
		if _, err := client.JoinedMembers(ctx, roomID); err != nil {
			slog.Warn("matrix: seed state store: joined members", "room", roomID, "error", err)
		}
	}
}

// handleEncrypted decrypts a timeline m.room.encrypted event and forwards
// the decrypted message. If no room key is available it waits briefly for
// keys (the machine handles incoming m.room_key to-device events), then
// requests the key from the sender. Undecryptable events are logged with a
// reason — never silently dropped.
func (p *MatrixPoller) handleEncrypted(ctx context.Context, evt *event.Event) {
	if evt.Sender == id.UserID(p.cfg.UserID) {
		return
	}
	mach := p.e2ee.Mach()
	content := evt.Content.AsEncrypted()
	if content == nil {
		slog.Warn("matrix: encrypted event has no content", "room", evt.RoomID, "sender", evt.Sender)
		return
	}
	if content.Algorithm != id.AlgorithmMegolmV1 {
		slog.Warn("matrix: unsupported encryption algorithm", "room", evt.RoomID, "sender", evt.Sender, "algorithm", content.Algorithm)
		return
	}

	decrypted, err := mach.DecryptMegolmEvent(ctx, evt)
	if errors.Is(err, crypto.ErrNoSessionFound) {
		slog.Info("matrix: no session, waiting for keys", "room", evt.RoomID, "sender", evt.Sender, "session_id", content.SessionID)
		if mach.WaitForSession(ctx, evt.RoomID, content.SenderKey, content.SessionID, 3*time.Second) {
			if decrypted, err = mach.DecryptMegolmEvent(ctx, evt); err != nil {
				slog.Warn("matrix decrypt after wait", "room", evt.RoomID, "error", err)
				p.manager.RecordPollHealth("matrix", err)
				return
			}
		} else {
			p.requestKeys(ctx, evt, content)
			if !mach.WaitForSession(ctx, evt.RoomID, content.SenderKey, content.SessionID, 22*time.Second) {
				err := errors.New("undecryptable message: no session after key request")
				slog.Warn("matrix: still no session after key request; message undecryptable",
					"room", evt.RoomID, "sender", evt.Sender, "session_id", content.SessionID)
				p.manager.RecordPollHealth("matrix", err)
				return
			}
			if decrypted, err = mach.DecryptMegolmEvent(ctx, evt); err != nil {
				slog.Warn("matrix decrypt after key request", "room", evt.RoomID, "error", err)
				p.manager.RecordPollHealth("matrix", err)
				return
			}
		}
	} else if err != nil {
		var withheld *event.RoomKeyWithheldEventContent
		if errors.As(err, &withheld) {
			slog.Warn("matrix key withheld", "room", evt.RoomID, "sender", evt.Sender, "code", withheld.Code, "reason", withheld.Reason)
		} else {
			slog.Warn("matrix decrypt failed", "room", evt.RoomID, "sender", evt.Sender, "error", err)
		}
		p.manager.RecordPollHealth("matrix", err)
		return
	}
	if decrypted == nil {
		slog.Warn("matrix: decrypt returned nil event", "room", evt.RoomID)
		return
	}
	if decrypted.Type == event.EventMessage {
		p.handleMessage(ctx, decrypted)
	}
}

// requestKeys asks the sender (and our own devices) for the room key for a
// session we couldn't decrypt.
func (p *MatrixPoller) requestKeys(ctx context.Context, evt *event.Event, content *event.EncryptedEventContent) {
	users := map[id.UserID][]id.DeviceID{
		evt.Sender:             {"*"},
		p.e2ee.Client().UserID: {"*"},
	}
	if err := p.e2ee.Mach().SendRoomKeyRequest(ctx, evt.RoomID, content.SenderKey, content.SessionID, "", users); err != nil {
		slog.Warn("matrix key request failed", "room", evt.RoomID, "error", err)
	}
}

// handleMessage forwards a plaintext (or decrypted) message event to the
// manager's inbound path.
func (p *MatrixPoller) handleMessage(ctx context.Context, evt *event.Event) {
	if evt.Sender == id.UserID(p.cfg.UserID) {
		return
	}
	body := evt.Content.AsMessage()
	text := ""
	if body != nil {
		text = strings.TrimSpace(body.Body)
	}
	if text == "" {
		return
	}
	if _, err := p.manager.Inbound(ctx, InboundMessage{
		Channel:   ChannelMatrix,
		Sender:    evt.Sender.String(),
		Text:      text,
		ThreadRef: evt.RoomID.String(),
	}); err != nil {
		slog.Warn("matrix inbound", "room", evt.RoomID, "sender", evt.Sender, "error", err)
	}
}
