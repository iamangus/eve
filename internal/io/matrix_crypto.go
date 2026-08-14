package io

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/crypto/olm"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// MatrixE2EE wraps the mautrix client and Olm machine for the matrix
// channel. It owns the JSON crypto store (Olm account, inbound/outbound
// Megolm sessions, device list, sync token) so decryption and key state
// survive restarts. It must be built with -tags goolm for the pure-Go Olm
// implementation; the store is a plain JSON file under DATA_DIR, so no
// database is required.
type MatrixE2EE struct {
	client *mautrix.Client
	mach   *crypto.OlmMachine
	store  *jsonCryptoStore
	log    zerolog.Logger
}

// matrixDBName is the JSON file (relative to DataDir) holding all crypto
// state for the matrix channel.
const matrixDBName = "eve_matrix.json"

// NewMatrixE2EE connects to the homeserver, discovers the real device ID
// (the compat token maps to a fixed device), opens the crypto store, and
// prepares the Olm machine. It must be called before the poller starts.
func NewMatrixE2EE(ctx context.Context, cfg MatrixConfig, dataDir string, pickleKey []byte) (*MatrixE2EE, error) {
	if len(pickleKey) == 0 {
		return nil, fmt.Errorf("matrix: pickle key must not be empty")
	}
	client, err := mautrix.NewClient(cfg.Homeserver, id.UserID(cfg.UserID), cfg.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("matrix: client: %w", err)
	}

	// The compatibility token is bound to a specific device. Ask the server
	// which one it is so device keys and to-device messages route correctly.
	whoami, err := client.Whoami(ctx)
	if err != nil {
		return nil, fmt.Errorf("matrix: whoami: %w", err)
	}
	client.UserID = whoami.UserID
	client.DeviceID = whoami.DeviceID
	slog.Info("matrix e2ee", "user", whoami.UserID, "device", whoami.DeviceID)

	store, err := openJSONCryptoStore(filepath.Join(dataDir, matrixDBName), pickleKey)
	if err != nil {
		return nil, fmt.Errorf("matrix: open store: %w", err)
	}
	client.Store = store
	client.StateStore = store.stateStore

	log := zerolog.New(os.Stderr).With().Timestamp().Logger()

	mach := crypto.NewOlmMachine(client, &log, store.ms, store.stateStore)
	if err := mach.Load(ctx); err != nil {
		return nil, fmt.Errorf("matrix: olm machine load: %w", err)
	}

	// Share the account device keys (and top up one-time keys) with the
	// homeserver so the owner's clients can start Olm sessions with us.
	if err := mach.ShareKeys(ctx, -1); err != nil {
		return nil, fmt.Errorf("matrix: share keys: %w", err)
	}

	e2ee := &MatrixE2EE{client: client, mach: mach, store: store, log: log}
	// Route outgoing sends through the crypto helper so messages into
	// encrypted rooms are encrypted automatically.
	client.Crypto = &matrixCryptoHelper{e2ee: e2ee}
	return e2ee, nil
}

// Client exposes the mautrix client (used for sending via the adapter).
func (e *MatrixE2EE) Client() *mautrix.Client { return e.client }

// Mach exposes the Olm machine (used by the poller for decryption).
func (e *MatrixE2EE) Mach() *crypto.OlmMachine { return e.mach }

// Close flushes the crypto store.
func (e *MatrixE2EE) Close() error {
	if e.store != nil {
		return e.store.Flush(context.Background())
	}
	return nil
}

// crossSigningSetupTimeout bounds how long EnsureCrossSigningSetup waits for
// the account owner to approve the reset at the account-management URL.
// Synapse grants a 10-minute replacement window, so 15 minutes is a generous
// upper bound that still fails loudly instead of hanging forever.
const crossSigningSetupTimeout = 15 * time.Minute

// crossSigningRetryInterval is how often EnsureCrossSigningSetup retries the
// key upload while waiting for the owner's approval.
const crossSigningRetryInterval = 5 * time.Second

// EnsureCrossSigningSetup makes Eve's bot the account's trusted cross-signing
// client. It is idempotent: when the current device is already signed by the
// account's self-signing key it returns immediately.
//
// When the account has cross-signing keys owned by another (orphaned) client
// — the incognito browser session that provisioned the account — the
// homeserver refuses the replacement with a 401 whose m.oauth params carry
// the account-management approval URL (the ESS "reset cross-signing" flow).
// surfaceURL is called with that URL so the account owner can approve the
// reset in a browser; the method then retries until the upload succeeds
// within Synapse's temporary replacement window or times out. No admin token
// and no permanent permission change are involved — a one-time approval by
// the account owner is all that is required.
//
// The caller runs this as a goroutine: it blocks for up to crossSigningSetupTimeout
// when approval is pending. On success the bot's device is cross-signed by the
// new self-signing key and Element no longer flags its messages.
func (e *MatrixE2EE) EnsureCrossSigningSetup(ctx context.Context, surfaceURL func(string)) error {
	hasKeys, verified, err := e.mach.GetOwnVerificationStatus(ctx)
	if err != nil {
		return fmt.Errorf("matrix: check cross-signing status: %w", err)
	}
	if verified {
		e.log.Info().Bool("has_keys", hasKeys).Msg("cross-signing already set up and device verified")
		return nil
	}
	e.log.Info().Bool("has_keys", hasKeys).Msg("cross-signing not verified; generating keys")

	keys, err := e.mach.GenerateCrossSigningKeys()
	if err != nil {
		return fmt.Errorf("matrix: generate cross-signing keys: %w", err)
	}

	deadline := time.Now().Add(crossSigningSetupTimeout)
	lastURL := ""
	for {
		err := e.mach.PublishCrossSigningKeys(ctx, keys, nil)
		if err == nil {
			break
		}
		url := crossSigningApprovalURL(err)
		if url == "" {
			return fmt.Errorf("matrix: publish cross-signing keys: %w", err)
		}
		if url != lastURL {
			lastURL = url
			e.log.Warn().Str("url", url).Str("window", "10 minutes").Msg("matrix cross-signing reset requires approval")
			surfaceURL(url)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("matrix: cross-signing reset not approved within %s (approve at %s)", crossSigningSetupTimeout, url)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(crossSigningRetryInterval):
		}
	}

	// The upload succeeded: sign the current device and the master key with
	// the new self-signing key so the bot is trusted by the account.
	if err := e.mach.SignOwnDevice(ctx, e.mach.OwnIdentity()); err != nil {
		return fmt.Errorf("matrix: sign own device: %w", err)
	}
	if err := e.mach.SignOwnMasterKey(ctx); err != nil {
		return fmt.Errorf("matrix: sign own master key: %w", err)
	}
	e.log.Info().Msg("cross-signing set up; Eve is now the trusted client")
	return nil
}

// crossSigningApprovalURL extracts the account-management approval URL from a
// 401 response. Under MAS/Synapse the response body carries the URL in
// params.m.oauth.url (and the unstable org.matrix.cross_signing_reset form);
// an empty result means the error is not the approval refusal.
func crossSigningApprovalURL(err error) string {
	var httpErr mautrix.HTTPError
	if !errors.As(err, &httpErr) || !httpErr.IsStatus(http.StatusUnauthorized) {
		return ""
	}
	var ui mautrix.RespUserInteractive
	if jerr := json.Unmarshal([]byte(httpErr.ResponseBody), &ui); jerr != nil {
		return ""
	}
	for _, stage := range []mautrix.AuthType{mautrix.AuthTypeOAuth, mautrix.AuthType("org.matrix.cross_signing_reset")} {
		raw, ok := ui.Params[stage]
		if !ok {
			continue
		}
		params, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if u, ok := params["url"].(string); ok && u != "" {
			return u
		}
	}
	return ""
}

// matrixCryptoHelper adapts the OlmMachine to the mautrix CryptoHelper
// interface so client.SendText auto-encrypts into encrypted rooms (and stays
// plaintext elsewhere). Sharing a group session with the room's members is
// done lazily on the first message per session.
type matrixCryptoHelper struct {
	e2ee *MatrixE2EE
}

var _ mautrix.CryptoHelper = (*matrixCryptoHelper)(nil)

func (h *matrixCryptoHelper) Init(ctx context.Context) error { return nil }

func (h *matrixCryptoHelper) Encrypt(ctx context.Context, roomID id.RoomID, evtType event.Type, content any) (*event.EncryptedEventContent, error) {
	mach := h.e2ee.Mach()
	encrypted, err := mach.EncryptMegolmEventWithStateKey(ctx, roomID, evtType, nil, content)
	if errors.Is(err, crypto.ErrNoGroupSession) || errors.Is(err, crypto.ErrSessionExpired) || errors.Is(err, crypto.ErrSessionNotShared) {
		users, uerr := h.e2ee.Client().StateStore.GetRoomJoinedOrInvitedMembers(ctx, roomID)
		if uerr != nil {
			return nil, fmt.Errorf("matrix: get members for session: %w", uerr)
		}
		if serr := mach.ShareGroupSession(ctx, roomID, users); serr != nil {
			return nil, fmt.Errorf("matrix: share group session: %w", serr)
		}
		encrypted, err = mach.EncryptMegolmEventWithStateKey(ctx, roomID, evtType, nil, content)
	}
	if err != nil {
		return nil, err
	}
	return encrypted, nil
}

func (h *matrixCryptoHelper) Decrypt(ctx context.Context, evt *event.Event) (*event.Event, error) {
	return h.e2ee.Mach().DecryptMegolmEvent(ctx, evt)
}

func (h *matrixCryptoHelper) WaitForSession(ctx context.Context, roomID id.RoomID, senderKey id.SenderKey, sessionID id.SessionID, timeout time.Duration) bool {
	return h.e2ee.Mach().WaitForSession(ctx, roomID, senderKey, sessionID, timeout)
}

func (h *matrixCryptoHelper) RequestSession(ctx context.Context, roomID id.RoomID, senderKey id.SenderKey, sessionID id.SessionID, userID id.UserID, deviceID id.DeviceID) {
	if deviceID == "" {
		deviceID = "*"
	}
	_ = h.e2ee.Mach().SendRoomKeyRequest(ctx, roomID, senderKey, sessionID, "", map[id.UserID][]id.DeviceID{
		userID: {deviceID},
	})
}

// ---------------------------------------------------------------------------
// JSON crypto store
//
// mautrix ships only a SQL store. To keep eve free of SQLite, we implement
// the crypto.Store interface with a plain JSON file. The Olm objects are
// pickled with the same pickle key the SQL store uses; the result is written
// atomically on every mutation (the MemoryStore save callback fires after
// each write while holding its lock). A snapshot (jsonStoreSnapshot) is
// loaded on startup and restored into the MemoryStore.
//
// The sync token (next_batch) and filter ID are kept in a tiny sidecar meta
// file rather than the main snapshot, because the mautrix client saves them
// from the sync goroutine *without* holding the MemoryStore lock, while the
// send goroutine may be mutating the store concurrently. Writing the meta
// sidecar avoids reading any of the crypto maps on that path, so there is no
// race. On restore the meta file is authoritative for those two values.
// ---------------------------------------------------------------------------

type jsonCryptoStore struct {
	ms        *crypto.MemoryStore
	path      string
	pickleKey []byte

	filterID   string
	nextBatch  string
	stateStore *mautrix.MemoryStateStore

	// snapshotMu guards the on-disk files against concurrent writers. The
	// main snapshot is written only from the save callback (which runs under
	// the MemoryStore lock) and from Flush at shutdown; the meta sidecar is
	// written by the SyncStore methods. Both are serialized through this mutex.
	snapshotMu sync.Mutex
}

type jsonStoreMeta struct {
	FilterID  string `json:"filter_id,omitempty"`
	NextBatch string `json:"next_batch,omitempty"`
}

func openJSONCryptoStore(path string, pickleKey []byte) (*jsonCryptoStore, error) {
	s := &jsonCryptoStore{path: path, pickleKey: pickleKey, stateStore: mautrix.NewMemoryStateStore().(*mautrix.MemoryStateStore)}
	s.ms = crypto.NewMemoryStore(func() error { return s.persist() })

	if data, err := os.ReadFile(path); err == nil {
		var snap jsonStoreSnapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			return nil, fmt.Errorf("matrix: parse crypto store %s: %w", path, err)
		}
		if err := s.restore(&snap); err != nil {
			return nil, fmt.Errorf("matrix: restore crypto store: %w", err)
		}
		if meta, mErr := os.ReadFile(path + ".meta"); mErr == nil {
			var m jsonStoreMeta
			if jErr := json.Unmarshal(meta, &m); jErr == nil {
				s.filterID = m.FilterID
				s.nextBatch = m.NextBatch
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("matrix: read crypto store %s: %w", path, err)
	}

	if s.ms.Account == nil {
		s.ms.Account = crypto.NewOlmAccount()
		s.ms.Account.Shared = false
		if err := s.persist(); err != nil {
			return nil, fmt.Errorf("matrix: initial crypto store write: %w", err)
		}
	}
	return s, nil
}

// persist snapshots the in-memory store and writes it atomically. It is safe
// to call while the MemoryStore lock is held (the save callback runs under
// it). It must not be called from the sync-goroutine SyncStore methods, which
// read no maps; those use persistMeta instead.
func (s *jsonCryptoStore) persist() error {
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()

	snap := s.snapshot()
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("matrix: encode crypto store: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("matrix: write crypto store tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("matrix: rename crypto store: %w", err)
	}
	return nil
}

// persistMeta writes the filter ID / sync token sidecar without touching any
// of the crypto maps, so it is safe to call from the sync goroutine even
// while a send goroutine mutates the store.
func (s *jsonCryptoStore) persistMeta() error {
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()

	data, err := json.Marshal(jsonStoreMeta{FilterID: s.filterID, NextBatch: s.nextBatch})
	if err != nil {
		return fmt.Errorf("matrix: encode crypto meta: %w", err)
	}
	tmp := s.path + ".meta.tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("matrix: write crypto meta tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path+".meta"); err != nil {
		return fmt.Errorf("matrix: rename crypto meta: %w", err)
	}
	return nil
}

// Flush is a no-op: every mutation already persists via the MemoryStore save
// callback (which runs under the store lock), so there is nothing to flush.
// A full snapshot here would race with concurrent send-goroutine mutations.
func (s *jsonCryptoStore) Flush(ctx context.Context) error {
	return nil
}

// SyncStore methods (SaveFilterID/LoadFilterID/SaveNextBatch/LoadNextBatch)
// satisfy mautrix.SyncStore so the jsonCryptoStore can be used as the
// client's Store. The values are persisted in a sidecar meta file.

func (s *jsonCryptoStore) SaveFilterID(ctx context.Context, userID id.UserID, filterID string) error {
	s.filterID = filterID
	return s.persistMeta()
}

func (s *jsonCryptoStore) LoadFilterID(ctx context.Context, userID id.UserID) (string, error) {
	return s.filterID, nil
}

func (s *jsonCryptoStore) SaveNextBatch(ctx context.Context, userID id.UserID, nextBatchToken string) error {
	s.nextBatch = nextBatchToken
	return s.persistMeta()
}

func (s *jsonCryptoStore) LoadNextBatch(ctx context.Context, userID id.UserID) (string, error) {
	return s.nextBatch, nil
}

// snapshot converts the in-memory store into a serializable snapshot. Called
// with the MemoryStore lock held (from the save callback) or from persist
// under snapshotMu.
func (s *jsonCryptoStore) snapshot() jsonStoreSnapshot {
	ms := s.ms
	snap := jsonStoreSnapshot{
		Account:              pickledAccount{},
		Sessions:             map[string][]pickledSession{},
		GroupSessions:        map[string]map[string]*pickledIGS{},
		WithheldGroupSessions: map[string]map[string]*event.RoomKeyWithheldEventContent{},
		OutGroupSessions:     map[string]*pickledOGS{},
		SharedGroupSessions:  map[string]map[string][]id.SessionID{},
		MessageIndices:       map[string]jsonMessageIndexValue{},
		Devices:              map[string]map[string]*id.Device{},
		CrossSigningKeys:     map[string]map[string]id.CrossSigningKey{},
		KeySignatures:        map[string]map[string]map[string]map[string]string{},
		OutdatedUsers:        []id.UserID{},
		Secrets:              map[string]string{},
		OlmHashes:            [][32]byte{},
	}

	if state, err := json.Marshal(s.stateStore); err == nil {
		snap.State = state
	}

	if ms.Account != nil {
		acc := ms.Account
		pickled, err := acc.Internal.Pickle(s.pickleKey)
		if err == nil {
			snap.Account = pickledAccount{
				Pickled:          base64.StdEncoding.EncodeToString(pickled),
				Shared:           acc.Shared,
				KeyBackupVersion: acc.KeyBackupVersion,
			}
		}
	}

	for senderKey, sessions := range ms.Sessions {
		list := []pickledSession{}
		for _, sess := range sessions {
			pickled, err := sess.Internal.Pickle(s.pickleKey)
			if err != nil {
				continue
			}
			list = append(list, pickledSession{
				Pickled:            base64.StdEncoding.EncodeToString(pickled),
				CreationTime:       sess.CreationTime,
				LastEncryptedTime:  sess.LastEncryptedTime,
				LastDecryptedTime:  sess.LastDecryptedTime,
			})
		}
		if len(list) > 0 {
			snap.Sessions[senderKey.String()] = list
		}
	}

	for roomID, room := range ms.GroupSessions {
		roomMap := map[string]*pickledIGS{}
		for sessionID, igs := range room {
			pickled, err := igs.Internal.Pickle(s.pickleKey)
			if err != nil {
				continue
			}
			roomMap[sessionID.String()] = &pickledIGS{
				Pickled:            base64.StdEncoding.EncodeToString(pickled),
				SigningKey:         igs.SigningKey,
				SenderKey:          igs.SenderKey,
				RoomID:             igs.RoomID,
				ForwardingChains:   igs.ForwardingChains,
				RatchetSafety:      igs.RatchetSafety,
				ReceivedAt:         igs.ReceivedAt,
				MaxAge:             igs.MaxAge,
				MaxMessages:        igs.MaxMessages,
				IsScheduled:        igs.IsScheduled,
				KeyBackupVersion:   igs.KeyBackupVersion,
				KeySource:          igs.KeySource,
			}
		}
		snap.GroupSessions[roomID.String()] = roomMap
	}

	for roomID, room := range ms.WithheldGroupSessions {
		roomMap := map[string]*event.RoomKeyWithheldEventContent{}
		for sessionID, withheld := range room {
			roomMap[sessionID.String()] = withheld
		}
		snap.WithheldGroupSessions[roomID.String()] = roomMap
	}

	for roomID, ogs := range ms.OutGroupSessions {
		pickled, err := ogs.Internal.Pickle(s.pickleKey)
		if err != nil {
			continue
		}
		users := map[string]map[string]int{}
		for ud, state := range ogs.Users {
			if users[ud.UserID.String()] == nil {
				users[ud.UserID.String()] = map[string]int{}
			}
			users[ud.UserID.String()][ud.DeviceID.String()] = int(state)
		}
		snap.OutGroupSessions[roomID.String()] = &pickledOGS{
			Pickled:            base64.StdEncoding.EncodeToString(pickled),
			MaxMessages:        ogs.MaxMessages,
			MessageCount:       ogs.MessageCount,
			Users:              users,
			RoomID:             ogs.RoomID,
			Shared:             ogs.Shared,
			CreationTime:       ogs.CreationTime,
			LastEncryptedTime:  ogs.LastEncryptedTime,
		}
	}

	for userID, idkMap := range ms.SharedGroupSessions {
		inner := map[string][]id.SessionID{}
		for idk, sessions := range idkMap {
			list := make([]id.SessionID, 0, len(sessions))
			for sid := range sessions {
				list = append(list, sid)
			}
			inner[idk.String()] = list
		}
		snap.SharedGroupSessions[userID.String()] = inner
	}

	for key, val := range ms.MessageIndices {
		snap.MessageIndices[fmt.Sprintf("%s:%d", key.SessionID, key.Index)] = jsonMessageIndexValue{
			EventID:   val.EventID,
			Timestamp: val.Timestamp,
		}
	}

	for userID, devices := range ms.Devices {
		deviceMap := map[string]*id.Device{}
		for deviceID, dev := range devices {
			deviceMap[deviceID.String()] = dev
		}
		snap.Devices[userID.String()] = deviceMap
	}

	for userID, usageMap := range ms.CrossSigningKeys {
		inner := map[string]id.CrossSigningKey{}
		for usage, key := range usageMap {
			inner[string(usage)] = key
		}
		snap.CrossSigningKeys[userID.String()] = inner
	}

	for userID, keyMap := range ms.KeySignatures {
		inner := map[string]map[string]map[string]string{}
		for key, signers := range keyMap {
			signerMap := map[string]map[string]string{}
			for signerUser, sigKeys := range signers {
				sigKeyMap := map[string]string{}
				for sigKey, val := range sigKeys {
					sigKeyMap[sigKey.String()] = val
				}
				signerMap[signerUser.String()] = sigKeyMap
			}
			inner[key.String()] = signerMap
		}
		snap.KeySignatures[userID.String()] = inner
	}

	for userID := range ms.OutdatedUsers {
		snap.OutdatedUsers = append(snap.OutdatedUsers, userID)
	}

	for name, value := range ms.Secrets {
		snap.Secrets[name.String()] = value
	}

	if ms.OlmHashes != nil {
		for hash := range ms.OlmHashes.Iter() {
			snap.OlmHashes = append(snap.OlmHashes, hash)
		}
	}

	return snap
}

// restore loads a snapshot into the in-memory MemoryStore. Called at startup
// before the machine is used.
func (s *jsonCryptoStore) restore(snap *jsonStoreSnapshot) error {
	ms := s.ms
	if snap.State != nil {
		if err := json.Unmarshal(snap.State, s.stateStore); err != nil {
			return fmt.Errorf("matrix: decode state store: %w", err)
		}
	}

	if snap.Account.Pickled != "" {
		pickled, err := base64.StdEncoding.DecodeString(snap.Account.Pickled)
		if err != nil {
			return fmt.Errorf("matrix: decode account: %w", err)
		}
		acc, err := olm.AccountFromPickled(pickled, s.pickleKey)
		if err != nil {
			return fmt.Errorf("matrix: unpickle account: %w", err)
		}
		ms.Account = &crypto.OlmAccount{
			Internal:         acc,
			Shared:           snap.Account.Shared,
			KeyBackupVersion: snap.Account.KeyBackupVersion,
		}
	}

	for senderKey, sessions := range snap.Sessions {
		list := crypto.OlmSessionList{}
		for _, sess := range sessions {
			pickled, err := base64.StdEncoding.DecodeString(sess.Pickled)
			if err != nil {
				continue
			}
			internal, err := olm.SessionFromPickled(pickled, s.pickleKey)
			if err != nil {
				continue
			}
			list = append(list, &crypto.OlmSession{
				Internal: internal,
				ExpirationMixin: crypto.ExpirationMixin{
					TimeMixin: crypto.TimeMixin{
						CreationTime:      sess.CreationTime,
						LastEncryptedTime: sess.LastEncryptedTime,
						LastDecryptedTime: sess.LastDecryptedTime,
					},
				},
			})
		}
		if len(list) > 0 {
			ms.Sessions[id.SenderKey(senderKey)] = list
		}
	}

	for roomID, room := range snap.GroupSessions {
		roomMap := map[id.SessionID]*crypto.InboundGroupSession{}
		for sessionID, igs := range room {
			pickled, err := base64.StdEncoding.DecodeString(igs.Pickled)
			if err != nil {
				continue
			}
			internal, err := olm.InboundGroupSessionFromPickled(pickled, s.pickleKey)
			if err != nil {
				continue
			}
			roomMap[id.SessionID(sessionID)] = &crypto.InboundGroupSession{
				Internal:         internal,
				SigningKey:       igs.SigningKey,
				SenderKey:        igs.SenderKey,
				RoomID:           igs.RoomID,
				ForwardingChains: igs.ForwardingChains,
				RatchetSafety:    igs.RatchetSafety,
				ReceivedAt:       igs.ReceivedAt,
				MaxAge:           igs.MaxAge,
				MaxMessages:      igs.MaxMessages,
				IsScheduled:      igs.IsScheduled,
				KeyBackupVersion: igs.KeyBackupVersion,
				KeySource:        igs.KeySource,
			}
		}
		ms.GroupSessions[id.RoomID(roomID)] = roomMap
	}

	for roomID, room := range snap.WithheldGroupSessions {
		roomMap := map[id.SessionID]*event.RoomKeyWithheldEventContent{}
		for sessionID, withheld := range room {
			roomMap[id.SessionID(sessionID)] = withheld
		}
		ms.WithheldGroupSessions[id.RoomID(roomID)] = roomMap
	}

	for roomID, ogs := range snap.OutGroupSessions {
		pickled, err := base64.StdEncoding.DecodeString(ogs.Pickled)
		if err != nil {
			continue
		}
		internal, err := olm.OutboundGroupSessionFromPickled(pickled, s.pickleKey)
		if err != nil {
			continue
		}
		users := map[crypto.UserDevice]crypto.OGSState{}
		for userID, devices := range ogs.Users {
			for deviceID, state := range devices {
				users[crypto.UserDevice{UserID: id.UserID(userID), DeviceID: id.DeviceID(deviceID)}] = crypto.OGSState(state)
			}
		}
		ms.OutGroupSessions[id.RoomID(roomID)] = &crypto.OutboundGroupSession{
			Internal: internal,
			ExpirationMixin: crypto.ExpirationMixin{
				TimeMixin: crypto.TimeMixin{
					CreationTime:      ogs.CreationTime,
					LastEncryptedTime: ogs.LastEncryptedTime,
				},
			},
			MaxMessages:  ogs.MaxMessages,
			MessageCount: ogs.MessageCount,
			Users:        users,
			RoomID:       ogs.RoomID,
			Shared:       ogs.Shared,
		}
	}

	for userID, idkMap := range snap.SharedGroupSessions {
		inner := map[id.IdentityKey]map[id.SessionID]struct{}{}
		for idk, sessions := range idkMap {
			set := map[id.SessionID]struct{}{}
			for _, sid := range sessions {
				set[sid] = struct{}{}
			}
			inner[id.IdentityKey(idk)] = set
		}
		ms.SharedGroupSessions[id.UserID(userID)] = inner
	}

	for keyStr, val := range snap.MessageIndices {
		var sid id.SessionID
		var index uint
		if _, err := fmt.Sscanf(keyStr, "%s:%d", &sid, &index); err != nil {
			continue
		}
		// messageIndexKey is unexported in mautrix; the exported
		// ValidateMessageIndex is the supported way to re-add entries.
		_, _ = ms.ValidateMessageIndex(context.Background(), sid, val.EventID, index, val.Timestamp)
	}

	for userID, devices := range snap.Devices {
		deviceMap := map[id.DeviceID]*id.Device{}
		for deviceID, dev := range devices {
			deviceMap[id.DeviceID(deviceID)] = dev
		}
		ms.Devices[id.UserID(userID)] = deviceMap
	}

	for userID, usageMap := range snap.CrossSigningKeys {
		inner := map[id.CrossSigningUsage]id.CrossSigningKey{}
		for usage, key := range usageMap {
			inner[id.CrossSigningUsage(usage)] = key
		}
		ms.CrossSigningKeys[id.UserID(userID)] = inner
	}

	for userID, keyMap := range snap.KeySignatures {
		inner := map[id.Ed25519]map[id.UserID]map[id.Ed25519]string{}
		for key, signers := range keyMap {
			signerMap := map[id.UserID]map[id.Ed25519]string{}
			for signerUser, sigKeys := range signers {
				sigKeyMap := map[id.Ed25519]string{}
				for sigKey, val := range sigKeys {
					sigKeyMap[id.Ed25519(sigKey)] = val
				}
				signerMap[id.UserID(signerUser)] = sigKeyMap
			}
			inner[id.Ed25519(key)] = signerMap
		}
		ms.KeySignatures[id.UserID(userID)] = inner
	}

	for _, userID := range snap.OutdatedUsers {
		ms.OutdatedUsers[userID] = struct{}{}
	}

	for name, value := range snap.Secrets {
		ms.Secrets[id.Secret(name)] = value
	}

	for _, hash := range snap.OlmHashes {
		ms.OlmHashes.Add(hash)
	}

	return nil
}

// The snapshot DTO uses string keys for the crypto maps. id.SessionID etc.
// are string aliases, so plain string maps round-trip through JSON cleanly.

type jsonStoreSnapshot struct {
	State                json.RawMessage                                        `json:"state,omitempty"`
	Account              pickledAccount                                         `json:"account"`
	Sessions             map[string][]pickledSession                            `json:"sessions,omitempty"`
	GroupSessions        map[string]map[string]*pickledIGS                      `json:"group_sessions,omitempty"`
	WithheldGroupSessions map[string]map[string]*event.RoomKeyWithheldEventContent `json:"withheld,omitempty"`
	OutGroupSessions     map[string]*pickledOGS                                 `json:"out_group_sessions,omitempty"`
	SharedGroupSessions  map[string]map[string][]id.SessionID                   `json:"shared,omitempty"`
	MessageIndices       map[string]jsonMessageIndexValue                       `json:"message_indices,omitempty"`
	Devices              map[string]map[string]*id.Device                       `json:"devices,omitempty"`
	CrossSigningKeys     map[string]map[string]id.CrossSigningKey               `json:"cross_signing_keys,omitempty"`
	KeySignatures        map[string]map[string]map[string]map[string]string     `json:"key_signatures,omitempty"`
	OutdatedUsers        []id.UserID                                            `json:"outdated_users,omitempty"`
	Secrets              map[string]string                                      `json:"secrets,omitempty"`
	OlmHashes            [][32]byte                                             `json:"olm_hashes,omitempty"`
}

type pickledAccount struct {
	Pickled          string             `json:"pickled"`
	Shared           bool               `json:"shared"`
	KeyBackupVersion id.KeyBackupVersion `json:"key_backup_version,omitempty"`
}

type pickledSession struct {
	Pickled            string    `json:"pickled"`
	CreationTime       time.Time `json:"creation_time"`
	LastEncryptedTime  time.Time `json:"last_encrypted_time"`
	LastDecryptedTime  time.Time `json:"last_decrypted_time"`
}

type pickledIGS struct {
	Pickled            string                            `json:"pickled"`
	SigningKey         id.Ed25519                        `json:"signing_key"`
	SenderKey          id.Curve25519                     `json:"sender_key"`
	RoomID             id.RoomID                         `json:"room_id"`
	ForwardingChains   []string                          `json:"forwarding_chains,omitempty"`
	RatchetSafety      crypto.RatchetSafety              `json:"ratchet_safety,omitempty"`
	ReceivedAt         time.Time                         `json:"received_at,omitempty"`
	MaxAge             int64                             `json:"max_age"`
	MaxMessages        int                               `json:"max_messages"`
	IsScheduled        bool                              `json:"is_scheduled,omitempty"`
	KeyBackupVersion   id.KeyBackupVersion               `json:"key_backup_version,omitempty"`
	KeySource          id.KeySource                      `json:"key_source,omitempty"`
}

type pickledOGS struct {
	Pickled            string                           `json:"pickled"`
	MaxMessages        int                              `json:"max_messages"`
	MessageCount       int                              `json:"message_count"`
	Users              map[string]map[string]int        `json:"users,omitempty"`
	RoomID             id.RoomID                        `json:"room_id"`
	Shared             bool                             `json:"shared"`
	CreationTime       time.Time                        `json:"creation_time"`
	LastEncryptedTime  time.Time                        `json:"last_encrypted_time"`
}

type jsonMessageIndexValue struct {
	EventID   id.EventID `json:"event_id"`
	Timestamp int64      `json:"timestamp"`
}
