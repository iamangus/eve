package events

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/store"
)

type Handler struct {
	queue              *Queue
	store              *store.Store
	client             *agentfoundry.Client
	backendAgentID     string
	frontendAgentID    string
	backendMCPServers  []agentfoundry.MCPServer
	frontendMCPServers []agentfoundry.MCPServer
	token              string
}

func NewHandler(queue *Queue, st *store.Store, client *agentfoundry.Client, backendAgentID, frontendAgentID, token string, backendMCPServers, frontendMCPServers []agentfoundry.MCPServer) *Handler {
	return &Handler{queue: queue, store: st, client: client, backendAgentID: backendAgentID, frontendAgentID: frontendAgentID, token: token, backendMCPServers: backendMCPServers, frontendMCPServers: frontendMCPServers}
}

// EnqueueFrontend lets the backend agent hand a hidden trigger to Frontend
// Eve. It is deliberately not a user-visible chat message.
func (h *Handler) EnqueueFrontend(content, conversationID string) error {
	if conversationID == "" {
		conversationID = h.store.PrimaryConversationID()
	}
	payload, err := json.Marshal(map[string]string{"conversation_id": conversationID, "content": content})
	if err != nil {
		return err
	}
	_, err = h.queue.Enqueue("backend_eve", "frontend", randomID(), payload)
	return err
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	if h.token != "" {
		mux.HandleFunc("POST /api/inbound/opendev", h.openDev)
	}
}

func (h *Handler) openDev(w http.ResponseWriter, r *http.Request) {
	if !validToken(r, h.token) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad token"})
		return
	}
	var in struct {
		ID       string          `json:"id"`
		Category string          `json:"category"`
		Payload  json.RawMessage `json:"payload"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || (in.Category != "backend" && in.Category != "frontend") || in.ID == "" || !json.Valid(in.Payload) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id, category (backend or frontend), and payload are required"})
		return
	}
	queued, err := h.queue.Enqueue("opendev", in.Category, in.ID, in.Payload)
	if err != nil {
		slog.Error("enqueue opendev event", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "queue unavailable"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": queued})
}

func (h *Handler) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := h.processOne(ctx); err != nil {
			slog.Warn("process internal event", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *Handler) processOne(ctx context.Context) error {
	event, err := h.queue.Lease(time.Now().UTC(), time.Minute)
	if err != nil || event == nil {
		return err
	}
	if err := h.deliver(ctx, event); err != nil {
		if retryErr := h.queue.Retry(event); retryErr != nil {
			return fmt.Errorf("deliver: %v; release: %w", err, retryErr)
		}
		return err
	}
	return h.queue.Complete(event)
}

func (h *Handler) deliver(ctx context.Context, event *Event) error {
	scope := "backend"
	mcpServers := h.backendMCPServers
	agentID := h.backendAgentID
	if event.Category == "frontend" {
		var payload struct {
			ConversationID string `json:"conversation_id"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.ConversationID == "" {
			return errors.New("frontend event payload requires conversation_id")
		}
		scope = "frontend:" + payload.ConversationID
		mcpServers = h.frontendMCPServers
		agentID = h.frontendAgentID
	}
	runID := h.store.PersistentRun(scope)
	if runID == "" {
		var err error
		runID, err = h.client.CreatePersistentRun(ctx, agentID, agentfoundry.PersistentRunOptions{MCPServers: mcpServers})
		if err != nil {
			return err
		}
		if err := h.store.SetPersistentRun(scope, runID); err != nil {
			return err
		}
	}
	message, err := json.Marshal(map[string]any{"source": event.Source, "category": event.Category, "payload": json.RawMessage(event.Payload), "hidden": true})
	if err != nil {
		return err
	}
	_, err = h.client.SendPersistentRunInput(ctx, runID, string(message), event.ID)
	return err
}

func validToken(r *http.Request, expected string) bool {
	token := r.Header.Get("Authorization")
	if strings.HasPrefix(token, "Bearer ") {
		token = strings.TrimPrefix(token, "Bearer ")
	} else {
		token = r.URL.Query().Get("token")
	}
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
