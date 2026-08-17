package io

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/store"
)

// ErrNoEndpoint is returned when no output-capable endpoint exists to deliver
// a message through.
var ErrNoEndpoint = errors.New("no reachable output endpoint")

// ErrInvalidChoice is returned when the routing agent returns an unusable
// endpoint id.
var ErrInvalidChoice = errors.New("routing agent returned an invalid endpoint")

// ErrNoAdapter is returned when no adapter is registered for a chosen channel.
var ErrNoAdapter = errors.New("no adapter registered for channel")

// ErrBusy is returned when a proactive send is attempted while the
// conversation already has a run in flight (user is mid-conversation).
var ErrBusy = errors.New("conversation is busy")

// ErrDisabled is returned when proactive sends are disabled by config.
var ErrDisabled = errors.New("proactive sends are disabled")

// Adapter delivers canonical outbound messages through a specific medium.
// Each channel type has exactly one adapter.
type Adapter interface {
	Type() ChannelType
	Send(ctx context.Context, msg OutboundMessage) error
}

// Router is the send pipeline. It decides which endpoint a message should go
// through (mechanical rules + the routing agent), then dispatches to the
// matching adapter. Web chat replies short-circuit past this entirely (the
// per-run SSE proxy streams them); the router governs async replies and all
// proactive sends.
type Router struct {
	reg              *Registry
	hub              *Hub
	ident            *Resolver
	store            *store.Store
	client           *agentfoundry.Client
	routerAgentID    string
	proactiveEnabled bool

	adapters map[ChannelType]Adapter
}

type RouterOptions struct {
	RouterAgentID    string
	AssistantAgentID string
	ProactiveEnabled bool
	RouterTimeout    time.Duration
}

func NewRouter(reg *Registry, hub *Hub, ident *Resolver, st *store.Store, client *agentfoundry.Client, opts RouterOptions) *Router {
	r := &Router{
		reg:              reg,
		hub:              hub,
		ident:            ident,
		store:            st,
		client:           client,
		routerAgentID:    opts.RouterAgentID,
		proactiveEnabled: opts.ProactiveEnabled,
		adapters:         make(map[ChannelType]Adapter),
	}
	r.RegisterAdapter(&webAdapter{store: st, hub: hub})
	return r
}

func (r *Router) RegisterAdapter(a Adapter) {
	r.adapters[a.Type()] = a
}

// Notify is the proactive entry point: deliver a message Eve wants to send
// without the user having just spoken (task completions, notifications,
// reminders). forceChannel optionally pins the destination channel; empty
// lets the router decide. Delivery is dispatched to the chosen adapter.
func (r *Router) Notify(ctx context.Context, convID, content, purpose, forceChannel string) error {
	if !r.proactiveEnabled {
		return ErrDisabled
	}
	// Never interrupt a conversation that has a run in flight: the user is
	// mid-exchange with Eve. The task board keeps the state visible so Eve
	// can surface it naturally on a later turn.
	if runID, _ := r.store.ActiveRunForConversation(convID); runID != "" {
		return ErrBusy
	}
	participants, err := r.store.ConversationParticipants(convID)
	if err != nil {
		participants = []string{"owner"}
	}
	req := SendRequest{
		ConversationID: convID,
		Content:        content,
		Purpose:        purpose,
		Participants:   participants,
		ForceChannel:   forceChannel,
	}
	dest, err := r.Decide(ctx, req)
	if err != nil {
		return err
	}
	return r.Deliver(ctx, req, dest)
}

// Reply is the mechanical reply path: deliver Eve's answer back to the
// channel the user's message came in on. Unlike Notify it has no proactive or
// busy gate — a reply is expected, not an interruption. originThread and
// recipient pin the destination (matrix room / sender address) when the
// origin channel needs them; the adapter falls back to the channel's default
// recipient otherwise.
func (r *Router) Reply(ctx context.Context, convID, content string, origin ChannelType, originThread, recipient string) error {
	participants, err := r.store.ConversationParticipants(convID)
	if err != nil {
		participants = []string{"owner"}
	}
	req := SendRequest{
		ConversationID: convID,
		Content:        content,
		Purpose:        PurposeReply,
		Origin:         origin,
		OriginThread:   originThread,
		Recipient:      recipient,
		Participants:   participants,
	}
	dest, err := r.Decide(ctx, req)
	if err != nil {
		return err
	}
	return r.Deliver(ctx, req, dest)
}

// Decide determines the destination endpoint for a message. Rules, in order:
//  1. Multi-party conversation (any non-owner participant): pinned to the
//     origin thread so the other person receives the reply.
//  2. Explicit ForceChannel override.
//  3. Owner-only reply on an output-capable origin channel: echo back.
//  4. Proactive or non-output origin: ask the routing agent, else fall back
//     mechanically to the best reachable output endpoint.
func (r *Router) Decide(ctx context.Context, req SendRequest) (EndpointSnapshot, error) {
	if r.hasNonOwner(req.Participants) {
		if req.Origin != "" {
			if snap, ok := r.reg.Get(string(req.Origin)); ok && snap.Output {
				return snap, nil
			}
		}
		return EndpointSnapshot{}, ErrNoEndpoint
	}
	if req.ForceChannel != "" {
		if snap, ok := r.reg.Get(req.ForceChannel); ok && snap.Output {
			return snap, nil
		}
	}
	if req.Origin != "" {
		if snap, ok := r.reg.Get(string(req.Origin)); ok && snap.Output {
			return snap, nil
		}
	}
	if r.routerAgentID != "" {
		if snap, err := r.askRouterAgent(ctx, req); err == nil {
			return snap, nil
		} else {
			slog.Warn("routing agent", "error", err)
		}
	}
	return r.mechanicalFallback()
}

// Deliver formats the message for the chosen endpoint and dispatches through
// its adapter.
func (r *Router) Deliver(ctx context.Context, req SendRequest, dest EndpointSnapshot) error {
	adapter, ok := r.adapters[dest.Type]
	if !ok {
		return ErrNoAdapter
	}
	msg := OutboundMessage{
		Channel:        dest.Type,
		Recipient:      dest.DefaultRecipient,
		Text:           adaptContent(req.Content, dest),
		ThreadRef:      req.OriginThread,
		ConversationID: req.ConversationID,
	}
	if req.Recipient != "" {
		msg.Recipient = req.Recipient
	}
	return adapter.Send(ctx, msg)
}

func (r *Router) hasNonOwner(participants []string) bool {
	for _, p := range participants {
		if p == "" || p == "other" {
			return true
		}
		// "owner" is the persisted default for conversations created before
		// the owner identity became editable. It remains an owner alias after
		// that identity is renamed.
		if p == "owner" {
			continue
		}
		if !r.ident.IsOwner(p) {
			return true
		}
	}
	return false
}

// mechanicalFallback returns the highest-preference reachable output endpoint,
// or any output endpoint at all, or ErrNoEndpoint.
func (r *Router) mechanicalFallback() (EndpointSnapshot, error) {
	snaps := r.reg.Snapshot()
	for _, s := range snaps {
		if s.Output && s.ReachableNow() {
			return s, nil
		}
	}
	for _, s := range snaps {
		if s.Output {
			return s, nil
		}
	}
	return EndpointSnapshot{}, ErrNoEndpoint
}

// routerInput is the payload presented to the routing agent.
type routerInput struct {
	Purpose   string             `json:"purpose"`
	Content   string             `json:"content"`
	Endpoints []EndpointSnapshot `json:"endpoints"`
}

type routerOutput struct {
	EndpointID string `json:"endpoint_id"`
	Reason     string `json:"reason"`
}

const routerSystemPrompt = `You are Eve's message router. A message needs to be delivered to the user. You have been given the list of available channels (endpoints) with their capabilities and the user's current presence.

Choose the single best channel to deliver this message through. Consider:
- Purpose: a direct answer goes where the user is; a long report suits email; a quick status suits an active chat; a reminder suits something they will check soon.
- Presence: if the user is actively present on a channel, prefer it.
- Capabilities: respect whether a channel supports rich text or has length limits.

Reply with ONLY a JSON object of the form: {"endpoint_id": "...", "reason": "short explanation"}`

// askRouterAgent invokes the routing agent (a small model in agentfoundry)
// with a snapshot of available endpoints and the message to deliver. The
// result is validated mechanically before being accepted.
func (r *Router) askRouterAgent(ctx context.Context, req SendRequest) (EndpointSnapshot, error) {
	endpoints := r.reg.Snapshot()
	outputOnly := make([]EndpointSnapshot, 0, len(endpoints))
	for _, e := range endpoints {
		if e.Output {
			outputOnly = append(outputOnly, e)
		}
	}
	in := routerInput{
		Purpose:   req.Purpose,
		Content:   truncate(req.Content, 1500),
		Endpoints: outputOnly,
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return EndpointSnapshot{}, err
	}

	prompt := routerSystemPrompt + "\n\nChannel data (JSON):\n" + string(payload)

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	runID, err := r.client.RunAgent(cctx, r.routerAgentID, prompt, nil)
	if err != nil {
		return EndpointSnapshot{}, err
	}
	text, err := r.client.AwaitRunText(cctx, runID, 55*time.Second)
	if err != nil {
		return EndpointSnapshot{}, err
	}
	text = extractJSON(text)
	var out routerOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil || out.EndpointID == "" {
		return EndpointSnapshot{}, ErrInvalidChoice
	}
	if snap, ok := r.reg.Get(out.EndpointID); ok && snap.Output {
		return snap, nil
	}
	return EndpointSnapshot{}, ErrInvalidChoice
}

// adaptContent applies mechanical format adaptation for the target endpoint:
// plain-text channels get markdown stripped, and non-rich channels truncate
// very long messages. Adaptation is deterministic; the routing agent only
// picks the endpoint.
func adaptContent(s string, dest EndpointSnapshot) string {
	if !dest.RichText {
		s = stripMarkdown(s)
	}
	if !dest.RichText && len(s) > 4000 {
		s = s[:4000] + "…"
	}
	return s
}

func stripMarkdown(s string) string {
	repl := strings.NewReplacer(
		"**", "", "__", "",
		"```", "\n",
		"`", "",
		"* ", "• ",
		"# ", "",
		"## ", "",
	)
	return repl.Replace(s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// extractJSON pulls a JSON object out of model text that may include prose
// or code fences.
func extractJSON(s string) string {
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			return s[i : j+1]
		}
	}
	return s
}

// webAdapter delivers proactive/async messages to the web channel by
// appending them to the conversation and broadcasting to connected clients.
type webAdapter struct {
	store *store.Store
	hub   *Hub
}

func (a *webAdapter) Type() ChannelType { return ChannelWeb }

func (a *webAdapter) Send(ctx context.Context, msg OutboundMessage) error {
	m, err := a.store.AppendAssistantMessageReturn(msg.ConversationID, "", msg.Text, "web", "eve")
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
