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
	// EVEMCPChatURL is the chat MCP surface (task + calendar tools, no
	// send_message) attached to user-triggered inbound runs. Eve's reply is
	// delivered mechanically back to the origin channel.
	EVEMCPChatURL string
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
	Tasks           *tasks.Manager
	frontendTrigger func(content, conversationID string) error
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

// SetFrontendTrigger attaches the hidden Backend Eve -> Frontend Eve bridge.
func (m *Manager) SetFrontendTrigger(trigger func(content, conversationID string) error) {
	m.frontendTrigger = trigger
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
	if cfg.EVEMCPChatURL != "" {
		mcpSrv = append(mcpSrv, agentfoundry.MCPServer{
			Name:      "eve-chat",
			URL:       cfg.EVEMCPChatURL,
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

func (m *Manager) Inbound(ctx context.Context, msg InboundMessage) (runID string, err error) {
	ident := m.Ident.Resolve(msg.Channel, msg.Sender)
	if msg.TrustedOwner {
		ident = m.Ident.Owner()
	}
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
	owner := m.Ident.IsOwner(sender)
	var appended *store.Message
	if owner {
		var created bool
		appended, created, err = m.store.AppendQueuedOwnerMessageWithSource(convID, msg.Text, string(msg.Channel), sender, msg.ThreadRef, msg.Sender, msg.SourceID)
		if err == nil && !created {
			runID, drainErr := m.DrainPending(ctx, convID)
			if drainErr != nil {
				slog.Warn("retry existing queued owner message", "conv", convID, "message", appended.ID, "error", drainErr)
				return "", nil
			}
			return runID, nil
		}
	} else {
		appended, err = m.store.AppendUserMessageReturn(convID, msg.Text, string(msg.Channel), sender, msg.ThreadRef, msg.Sender)
	}
	if err != nil {
		return "", err
	}
	m.Hub.Broadcast(Event{Type: EventMessage, ConvID: convID, Data: appended})
	m.Reg.Touch(string(msg.Channel))
	if !owner {
		// Unknown / non-owner senders are persisted so Eve sees them, but no
		// run is triggered. Log loudly: a missing identities.json entry must
		// never look like the message vanished.
		slog.Warn("inbound from non-owner identity; no run triggered",
			"channel", msg.Channel, "sender", msg.Sender, "identity", sender)
		return "", nil
	}
	runID, err = m.DrainPending(ctx, convID)
	if err != nil {
		slog.Warn("owner message queued for retry", "conv", convID, "message", appended.ID, "error", err)
		return "", nil
	}
	return runID, nil
}

func (m *Manager) DrainPending(ctx context.Context, convID string) (string, error) {
	msg, err := m.store.PendingOwnerMessage(convID)
	if err != nil || msg == nil {
		return "", err
	}
	scope := "frontend-v2:" + convID
	runID := m.store.PersistentRun(scope)
	if runID == "" {
		history, err := m.BootstrapHistory(convID, msg.ID)
		if err != nil {
			return "", err
		}
		runID, err = m.client.CreatePersistentRun(ctx, m.assistantAgentID(), agentfoundry.PersistentRunOptions{MCPServers: m.mcpSrv, ClientKey: scope, History: history})
		if err != nil {
			return "", err
		}
		if err := m.store.SetPersistentRun(scope, runID); err != nil {
			return "", err
		}
	}
	claimed, err := m.store.ClaimPendingOwnerMessage(convID, runID, msg.ID)
	if err != nil || !claimed {
		return "", err
	}
	if err := m.submitOwnerInput(ctx, convID, runID, msg); err != nil {
		return runID, err
	}
	return runID, nil
}

func (m *Manager) BootstrapHistory(convID string, beforeID int64) ([]agentfoundry.Message, error) {
	history, _, err := m.renderHistoryBefore(convID, beforeID)
	if err != nil {
		return nil, err
	}
	return boundedHistory(m.withTaskBoard(history)), nil
}

func boundedHistory(history []agentfoundry.Message) []agentfoundry.Message {
	const maxMessages = 120
	const maxBytes = 256 * 1024
	start, size := len(history), 0
	for start > 0 && len(history)-start < maxMessages {
		next := len(fmt.Sprint(history[start-1].Content))
		if size+next > maxBytes && start < len(history) {
			break
		}
		size += next
		start--
	}
	selected := append([]agentfoundry.Message(nil), history[start:]...)
	if len(selected) > 0 && len(fmt.Sprint(selected[0].Content)) > maxBytes {
		text := fmt.Sprint(selected[0].Content)
		selected[0].Content = "[Earlier content truncated.]\n" + strings.ToValidUTF8(text[len(text)-maxBytes/2:], "")
	}
	if start == 0 {
		return selected
	}
	return append([]agentfoundry.Message{{Role: "user", Content: "[Earlier conversation context omitted to fit the run's initial history.]"}}, selected...)
}

func (m *Manager) RetryActiveInput(ctx context.Context, convID, runID string) error {
	msg, err := m.store.ActiveOwnerMessage(convID, runID)
	if err != nil {
		return err
	}
	return m.submitOwnerInput(ctx, convID, runID, msg)
}

// SteerActive injects a follow-up owner message into the conversation's
// running persistent-run turn. agentfoundry folds it in at the next LLM
// boundary, labeled with the source, instead of starting a later turn.
func (m *Manager) SteerActive(ctx context.Context, convID, runID string, msg *store.Message) error {
	inputID := fmt.Sprintf("chat-%s-%d", convID, msg.ID)
	_, err := m.client.SendPersistentRunInputWithMetadata(ctx, runID, msg.Content, inputID, map[string]string{"source": "web"})
	return err
}

func (m *Manager) submitOwnerInput(ctx context.Context, convID, runID string, msg *store.Message) error {
	inputID := fmt.Sprintf("chat-%s-%d", convID, msg.ID)
	_, err := m.client.SendPersistentRunInput(ctx, runID, msg.Content, inputID)
	if err == nil {
		return m.store.MarkActiveSubmitted(convID, runID, msg.ID)
	}
	if !agentfoundry.IsStaleRun(err) {
		return err
	}
	scope := "frontend-v2:" + convID
	history, historyErr := m.BootstrapHistory(convID, msg.ID)
	if historyErr != nil {
		return historyErr
	}
	replacement, createErr := m.client.CreatePersistentRun(ctx, m.assistantAgentID(), agentfoundry.PersistentRunOptions{MCPServers: m.mcpSrv, ClientKey: scope, History: history})
	if createErr != nil {
		return createErr
	}
	if err := m.store.ReplaceActiveOwnerRun(convID, runID, replacement, msg.ID); err != nil {
		return err
	}
	if err := m.store.SetPersistentRun(scope, replacement); err != nil {
		return err
	}
	_, err = m.client.SendPersistentRunInput(ctx, replacement, msg.Content, inputID)
	if err != nil {
		return err
	}
	return m.store.MarkActiveSubmitted(convID, replacement, msg.ID)
}

func (m *Manager) MigrateLegacyRuns(ctx context.Context) {
	for _, conv := range m.store.ListConversations() {
		old := m.store.PersistentRun("frontend:" + conv.ID)
		if old == "" || conv.ActiveRunID == old {
			continue
		}
		status, err := m.client.GetRun(ctx, old)
		if err != nil {
			slog.Warn("inspect legacy conversation run", "conv", conv.ID, "run", old, "error", err)
			continue
		}
		if status.Status == "running" {
			continue
		}
		if err := m.client.CancelRun(ctx, old); err != nil && !agentfoundry.IsNotFound(err) && !agentfoundry.IsStaleRun(err) {
			slog.Warn("cancel legacy conversation run", "conv", conv.ID, "run", old, "error", err)
			continue
		}
		if err := m.store.SetPersistentRun("frontend:"+conv.ID, ""); err != nil {
			slog.Warn("clear legacy conversation run", "conv", conv.ID, "error", err)
		}
	}
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

func (m *Manager) renderHistoryBefore(convID string, beforeID int64) ([]agentfoundry.Message, ctxmgr.RenderStats, error) {
	if m.ctxMgr != nil {
		if beforeID > 0 {
			return m.ctxMgr.RenderHistoryBefore(convID, beforeID)
		}
		return m.ctxMgr.RenderHistory(convID)
	}
	prior, err := m.store.ConversationHistory(convID)
	if err != nil {
		return nil, ctxmgr.RenderStats{}, err
	}
	history := make([]agentfoundry.Message, 0, len(prior))
	for _, msg := range prior {
		if beforeID > 0 && msg.ID >= beforeID {
			continue
		}
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
