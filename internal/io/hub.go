package io

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
)

// Event is pushed to browser clients over the persistent /api/events stream.
// It carries out-of-band updates that the per-run SSE proxy does not cover:
// proactively injected assistant messages, task state changes, channel and
// presence updates.
type Event struct {
	Type   string `json:"type"`
	ConvID string `json:"conversation_id,omitempty"`
	Data   any    `json:"data,omitempty"`
}

// Event types broadcast on the hub.
const (
	EventMessage    = "message"     // an assistant message was appended outside a run stream
	EventRunStatus  = "run_status"  // a run the client may care about changed state
	EventTaskUpdate = "task_update" // background task status changed
	EventChannels   = "channels"    // channel registry / presence changed
)

// Hub fans events out to long-lived SSE subscribers. It is a single-user
// assistant, so all events are broadcast to all subscribers and the client
// filters by conversation id.
type Hub struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: make(map[chan Event]struct{})}
}

func (h *Hub) Subscribe() (ch chan Event, cancel func()) {
	ch = make(chan Event, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (h *Hub) Broadcast(ev Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
			// Slow subscriber: drop rather than block the broadcaster.
		}
	}
}

// ServeHTTP implements GET /api/events as an SSE stream. Keep-alive pings and
// client disconnects are handled naturally: writing returns an error when the
// client goes away, ending the handler.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, cancel := h.Subscribe()
	defer cancel()

	// Write headers before the loop so the stream is usable immediately.
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			data, err := json.Marshal(ev)
			if err != nil {
				slog.Error("hub encode event", "error", err)
				continue
			}
			if _, err := w.Write([]byte("event: event\ndata: " + string(data) + "\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
