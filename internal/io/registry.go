package io

import (
	"sort"
	"sync"
	"time"
)

// Registry tracks every channel available to the IO manager: its static
// description (registered once at startup) plus runtime presence that
// adapters update as activity happens.
type Registry struct {
	mu              sync.RWMutex
	channels        map[string]*Channel
	presence        map[string]*Presence
	activityTimeout time.Duration
}

func NewRegistry(activityTimeout time.Duration) *Registry {
	return &Registry{
		channels:        make(map[string]*Channel),
		presence:        make(map[string]*Presence),
		activityTimeout: activityTimeout,
	}
}

// Register adds or replaces a channel description. Registration is idempotent;
// existing presence is preserved across re-registrations.
func (r *Registry) Register(ch Channel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.presence[ch.ID]; !ok {
		r.presence[ch.ID] = &Presence{}
	}
	r.channels[ch.ID] = &ch
}

// Touch records recent inbound/outbound activity on a channel and marks it
// connected (e.g. the web client is open and talking to us).
func (r *Registry) Touch(channelID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.presence[channelID]
	if !ok {
		return
	}
	p.Connected = true
	p.LastActivity = time.Now()
}

// SetConnected toggles a channel's connection state (e.g. web client
// heartbeat lapsed or a heartbeat arrived).
func (r *Registry) SetConnected(channelID string, connected bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.presence[channelID]
	if !ok {
		return
	}
	p.Connected = connected
	if connected {
		p.LastActivity = time.Now()
	}
}

// Snapshot returns the current endpoint state for every registered channel.
func (r *Registry) Snapshot() []EndpointSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]EndpointSnapshot, 0, len(r.channels))
	for id, ch := range r.channels {
		p := r.presence[id]
		snap := EndpointSnapshot{Channel: *ch, Presence: *p}
		if p.LastActivity.IsZero() {
			snap.Presence.LastActivity = time.Time{}
		}
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Preference > out[j].Preference })
	return out
}

// Get returns the endpoint snapshot for a single channel.
func (r *Registry) Get(channelID string) (EndpointSnapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ch, ok := r.channels[channelID]
	if !ok {
		return EndpointSnapshot{}, false
	}
	p := r.presence[channelID]
	return EndpointSnapshot{Channel: *ch, Presence: *p}, true
}

// Lookup resolves a recipient name to an endpoint snapshot, matching the
// channel's DefaultRecipient (e.g. an email address for the email channel).
func (r *Registry) Lookup(channelID, name string) (EndpointSnapshot, bool) {
	snap, ok := r.Get(channelID)
	if !ok {
		return EndpointSnapshot{}, false
	}
	if snap.DefaultRecipient != "" && snap.DefaultRecipient == name {
		return snap, true
	}
	return EndpointSnapshot{}, false
}

// Active returns true when the user was active on the channel recently
// (within activityTimeout), i.e. they are likely present right now.
func (r *Registry) Active(channelID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.presence[channelID]
	if !ok {
		return false
	}
	if !p.Connected {
		return false
	}
	return time.Since(p.LastActivity) < r.activityTimeout
}
