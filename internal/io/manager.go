package io

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/iamangus/eve/internal/agentfoundry"
	ctxmgr "github.com/iamangus/eve/internal/context"
	"github.com/iamangus/eve/internal/store"
	"github.com/iamangus/eve/internal/tasks"
)

// Config configures the IO manager.
type Config struct {
	DataDir          string
	ActivityTimeout  time.Duration
	RouterAgentID    string
	AssistantAgentID string
	ProactiveEnabled bool
	EVEMCPURL        string
}

// Manager is the IO layer of eve. It owns the channel registry, the event
// hub, the identity resolver, and the routing/send pipeline. Every medium
// (web today; email, matrix, sms, voice in later phases) plugs in here.
type Manager struct {
	Reg    *Registry
	Hub    *Hub
	Ident  *Resolver
	Router *Router
	store  *store.Store
	client *agentfoundry.Client
	mcpSrv []agentfoundry.MCPServer
	ctxMgr *ctxmgr.Manager
	agent  string
	// Tasks is the background-task manager, attached by main so the MCP task
	// tools and the task board can reach it.
	Tasks *tasks.Manager
	// Matrix holds the active matrix config (empty when disabled) so main can
	// start the sync poller.
	Matrix MatrixConfig
	// MatrixE2EE holds the mautrix client + crypto machine (nil when matrix
	// is disabled or crypto failed to initialize).
	MatrixE2EE *MatrixE2EE
	// Cal is the calendar store (nil when the calendar channel is disabled).
	Cal *CalStore

	healthMu sync.Mutex
	health   map[string]PollHealth
}

// PollHealth records the latest outcome of a background poller loop (email,
// matrix, calendar). lastCheck is when the poller last completed a cycle;
// lastError is empty when the last cycle succeeded.
type PollHealth struct {
	LastCheck time.Time `json:"last_check,omitempty"`
	LastError string    `json:"last_error,omitempty"`
}

// RecordPollHealth updates the health snapshot for a poller. A nil error
// clears any previous error; a non-nil error records it alongside the check
// timestamp so the UI can show when a path last failed.
func (m *Manager) RecordPollHealth(id string, err error) {
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	h := m.health[id]
	h.LastCheck = time.Now()
	if err != nil {
		h.LastError = err.Error()
	} else {
		h.LastError = ""
	}
	m.health[id] = h
}

func (m *Manager) healthSnapshot() map[string]PollHealth {
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	out := make(map[string]PollHealth, len(m.health))
	for id, h := range m.health {
		out[id] = h
	}
	return out
}

func NewManager(st *store.Store, client *agentfoundry.Client, cfg Config) (*Manager, error) {
	reg := NewRegistry(cfg.ActivityTimeout)
	reg.Register(Channel{
		ID:         "web",
		Type:       ChannelWeb,
		Name:       "Web UI",
		Input:      true,
		Output:     true,
		Streams:    true,
		RichText:   true,
		Preference: 100,
	})
	hub := NewHub()
	ident, err := LoadResolver(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	router := NewRouter(reg, hub, ident, st, client, RouterOptions{
		RouterAgentID:    cfg.RouterAgentID,
		AssistantAgentID: cfg.AssistantAgentID,
		ProactiveEnabled: cfg.ProactiveEnabled,
	})
	mcpSrv := []agentfoundry.MCPServer{}
	if cfg.EVEMCPURL != "" {
		mcpSrv = append(mcpSrv, agentfoundry.MCPServer{
			Name:      "eve",
			URL:       cfg.EVEMCPURL,
			Transport: "streamable-http",
		})
	}
	return &Manager{
		Reg:    reg,
		Hub:    hub,
		Ident:  ident,
		Router: router,
		store:  st,
		client: client,
		mcpSrv: mcpSrv,
		agent:  cfg.AssistantAgentID,
		health: make(map[string]PollHealth),
	}, nil
}

// RunPresenceLoop periodically marks channels with stale activity as
// disconnected so the presence badges stay truthful (e.g. a web tab closed
// without a goodbye heartbeat). Blocks until ctx is cancelled.
func (m *Manager) RunPresenceLoop(ctx context.Context) {
	if m.Reg.activityTimeout <= 0 {
		return
	}
	tick := m.Reg.activityTimeout / 2
	if tick < time.Second {
		tick = time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			changed := false
			for _, s := range m.Reg.Snapshot() {
				if !s.Presence.Connected {
					continue
				}
				if time.Since(s.Presence.LastActivity) >= m.Reg.activityTimeout {
					m.Reg.SetConnected(s.ID, false)
					changed = true
				}
			}
			if changed {
				m.Hub.Broadcast(Event{Type: EventChannels})
			}
		}
	}
}

func (m *Manager) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/events", m.Hub.ServeHTTP)
	mux.HandleFunc("POST /api/presence", m.presence)
	mux.HandleFunc("GET /api/channels", m.channels)
	mux.HandleFunc("POST /api/notify", m.notify)
	mux.HandleFunc("GET /api/identities", m.listIdentities)
	mux.HandleFunc("POST /api/identities", m.createIdentity)
	mux.HandleFunc("PUT /api/identities/{name}", m.updateIdentity)
	mux.HandleFunc("DELETE /api/identities/{name}", m.deleteIdentity)
}

func (m *Manager) listIdentities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"identities": m.Ident.List()})
}

func (m *Manager) createIdentity(w http.ResponseWriter, r *http.Request) {
	var in Identity
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if in.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	if err := m.Ident.Upsert(in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (m *Manager) updateIdentity(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var in Identity
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	// Identity names are immutable keys: the URL name wins.
	in.Name = name
	if err := m.Ident.Upsert(in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (m *Manager) deleteIdentity(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := m.Ident.Delete(name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

// PresenceSummary renders a compact human-readable snapshot of the user's
// presence for the decision engine and router agent.
func (m *Manager) PresenceSummary() string {
	snaps := m.Reg.Snapshot()
	var b strings.Builder
	for _, s := range snaps {
		fmt.Fprintf(&b, "%s: ", s.ID)
		if !s.Input && !s.Output {
			b.WriteString("offline")
		} else {
			if s.Output {
				b.WriteString("output")
			}
			if s.Presence.Connected {
				if b.Len() > 0 {
					b.WriteString("+")
				}
				b.WriteString("present")
			}
		}
		if s.Presence.LastActivity.IsZero() {
			b.WriteString(", no recent activity")
		} else {
			fmt.Fprintf(&b, ", last active %s ago", time.Since(s.Presence.LastActivity).Round(time.Second))
		}
		b.WriteString("; ")
	}
	return strings.TrimSuffix(b.String(), "; ")
}

// presence is the web client heartbeat: marks the web channel connected so
// routing decisions know the user is at the computer.
func (m *Manager) presence(w http.ResponseWriter, r *http.Request) {
	m.Reg.SetConnected("web", true)
	m.Hub.Broadcast(Event{Type: EventChannels})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// channels returns the current endpoint snapshot plus poller health (for the
// UI and debugging).
func (m *Manager) channels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"channels": m.Reg.Snapshot(),
		"health":   m.healthSnapshot(),
	})
}

// notify is the proactive-send surface: any system (or a manual curl) can
// hand Eve a message to deliver, and the router decides where it goes. It is
// also the target the MCP send_message tool funnels into.
func (m *Manager) notify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConversationID string `json:"conversation_id"`
		Content        string `json:"content"`
		Purpose        string `json:"purpose"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Content == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content is required"})
		return
	}
	if req.Purpose == "" {
		req.Purpose = PurposeNotification
	}
	convID := req.ConversationID
	if convID == "" {
		convID = m.store.PrimaryConversationID()
	}
	if err := m.Router.Notify(r.Context(), convID, req.Content, req.Purpose, ""); err != nil {
		slog.Warn("notify", "conv", convID, "purpose", req.Purpose, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Inbound routes a message that arrived through a channel adapter into a
// conversation and (when it is the owner speaking) triggers Eve's run. The
// web handler calls this for its own incoming messages; channel adapters
// (email, matrix, …) call it too so every mouth funnels through one path.
//
// Owner messages land in the primary conversation and start a run. Third
// party messages are appended to the owner conversation as user messages so
// Eve sees them, but no run is auto-triggered here — the calling adapter is
// responsible for what happens next (trigger handling etc.).
func (m *Manager) Inbound(ctx context.Context, msg InboundMessage) (runID string, err error) {
	ident := m.Ident.Resolve(msg.Channel, msg.Sender)
	sender := "owner"
	if ident != nil {
		sender = ident.Name
	}
	convID := m.store.PrimaryConversationID()
	if convID == "" {
		c, cerr := m.store.CreateConversation("Chat")
		if cerr != nil {
			return "", cerr
		}
		convID = c.ID
	}
	appended, err := m.store.AppendUserMessageReturn(convID, msg.Text, string(msg.Channel), sender, msg.ThreadRef, msg.Sender)
	if err != nil {
		return "", err
	}
	m.Hub.Broadcast(Event{Type: EventMessage, ConvID: convID, Data: appended})
	m.Reg.Touch(string(msg.Channel))
	if !m.Ident.IsOwner(sender) {
		// Unknown / non-owner senders are persisted so Eve sees them, but no
		// run is triggered. Log loudly: a missing identities.json entry must
		// never look like the message vanished.
		slog.Warn("inbound from non-owner identity; no run triggered",
			"channel", msg.Channel, "sender", msg.Sender, "identity", sender)
		return "", nil
	}
	history, _, herr := m.renderHistory(convID)
	if herr != nil {
		slog.Warn("render history", "conv", convID, "error", herr)
		history = nil
	}
	history = m.withTaskBoard(history)
	runID, err = m.client.RunAgentWith(ctx, m.assistantAgentID(), agentfoundry.RunOptions{
		Message:    msg.Text,
		History:    history,
		MCPServers: m.mcpSrv,
	})
	if err != nil {
		return "", err
	}
	if err := m.store.SetActiveRun(convID, runID); err != nil {
		slog.Warn("set active run", "conv", convID, "run", runID, "error", err)
	}
	return runID, nil
}

// InboundReply is the assistant-side counterpart of Inbound: it records a
// message Eve produced outside a web turn (e.g. an emailed reply) into the
// primary conversation and broadcasts it so connected clients see it live.
func (m *Manager) InboundReply(convID, content, channel, sender string) error {
	msg, err := m.store.AppendAssistantMessageReturn(convID, "", content, channel, sender)
	if err != nil {
		return err
	}
	m.Hub.Broadcast(Event{Type: EventMessage, ConvID: convID, Data: msg})
	return nil
}

// EnableEmail registers the email channel and its SMTP adapter. Called by
// main when SMTP config is present; a no-op otherwise.
func (m *Manager) EnableEmail(cfg SMTPConfig) {
	if !cfg.Enabled() {
		return
	}
	m.Reg.Register(Channel{
		ID:               "email",
		Type:             ChannelEmail,
		Name:             "Email",
		Input:            true,
		Output:           true,
		Streams:          false,
		RichText:         false,
		Reachable:        true,
		DefaultRecipient: cfg.From,
		Preference:       50,
	})
	m.Router.RegisterAdapter(&emailAdapter{cfg: cfg, reg: m.Reg, store: m.store, hub: m.Hub})
}

// EnableMatrix registers the matrix channel and its adapter. Called by main
// when matrix config is present; a no-op otherwise. e2ee carries the mautrix
// client and crypto machine so sending is encrypted in E2EE rooms.
func (m *Manager) EnableMatrix(cfg MatrixConfig, e2ee *MatrixE2EE) {
	if !cfg.Enabled() {
		return
	}
	m.Matrix = cfg
	m.MatrixE2EE = e2ee
	m.Reg.Register(Channel{
		ID:               "matrix",
		Type:             ChannelMatrix,
		Name:             "Matrix",
		Input:            true,
		Output:           true,
		Streams:          false,
		RichText:         true,
		Reachable:        true,
		DefaultRecipient: cfg.UserID,
		Preference:       30,
	})
	m.Router.RegisterAdapter(&matrixAdapter{cfg: cfg, reg: m.Reg, store: m.store, hub: m.Hub, e2ee: e2ee})
}

// EnableCalendar activates the CalDAV calendar channel. Called by main when
// calendar config is present; a no-op otherwise.
func (m *Manager) EnableCalendar(cfg CalDAVConfig) {
	if !cfg.Enabled() {
		return
	}
	m.Cal = NewCalStore(cfg)
}

// SetContext attaches the context manager so Inbound can render the same
// decayed, summarized history the web path uses. Called by main after both
// are constructed.
func (m *Manager) SetContext(cm *ctxmgr.Manager) {
	m.ctxMgr = cm
}

// SetTasks attaches the background-task manager (also reachable as Tasks).
func (m *Manager) SetTasks(tm *tasks.Manager) {
	m.Tasks = tm
}

func (m *Manager) assistantAgentID() string {
	return m.agent
}

func (m *Manager) renderHistory(convID string) ([]agentfoundry.Message, ctxmgr.RenderStats, error) {
	if m.ctxMgr != nil {
		return m.ctxMgr.RenderHistory(convID)
	}
	prior, err := m.store.ConversationHistory(convID)
	if err != nil {
		return nil, ctxmgr.RenderStats{}, err
	}
	history := make([]agentfoundry.Message, 0, len(prior))
	for _, msg := range prior {
		history = append(history, agentfoundry.Message{Role: msg.Role, Content: msg.Content})
	}
	return history, ctxmgr.RenderStats{}, nil
}

func (m *Manager) withTaskBoard(history []agentfoundry.Message) []agentfoundry.Message {
	if m.Tasks == nil {
		return history
	}
	board := m.Tasks.ContextBlock()
	if board == "" {
		return history
	}
	out := make([]agentfoundry.Message, 0, len(history)+1)
	out = append(out, agentfoundry.Message{Role: "user", Content: board})
	return append(out, history...)
}
