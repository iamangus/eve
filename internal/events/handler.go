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

var errAgentInputFailed = errors.New("agent input failed")

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
		mux.HandleFunc("GET /api/inbound/opendev/events/{id}", h.openDevStatus)
	}
}

func (h *Handler) openDevStatus(w http.ResponseWriter, r *http.Request) {
	if !validToken(r, h.token) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad token"})
		return
	}
	event, ok := h.queue.Status("opendev", r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "event not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": event.State, "agent_failures": event.AgentFailures, "last_error": event.LastError})
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
		if errors.Is(err, store.ErrNotFound) {
			if failErr := h.queue.Fail(event, err.Error()); failErr != nil {
				return fmt.Errorf("deliver: %v; record rejection: %w", err, failErr)
			}
			return err
		}
		if errors.Is(err, errAgentInputFailed) {
			if failErr := h.queue.AgentInputFailed(event, err.Error()); failErr != nil {
				return fmt.Errorf("deliver: %v; record failure: %w", err, failErr)
			}
			if event.AgentFailures >= 4 {
				slog.Error("internal event exhausted agent retries", "event", event.ID, "source", event.Source)
			}
			return err
		}
		if retryErr := h.queue.Retry(event); retryErr != nil {
			return fmt.Errorf("deliver: %v; release: %w", err, retryErr)
		}
		return err
	}
	return h.queue.Complete(event)
}

func (h *Handler) deliver(ctx context.Context, event *Event) error {
	inputID := event.InputID
	if inputID == "" {
		inputID = event.ID
	}
	scope := "backend"
	frontendConversationID := ""
	mcpServers := h.backendMCPServers
	agentID := h.backendAgentID
	if event.Category == "frontend" {
		var payload struct {
			ConversationID string `json:"conversation_id"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.ConversationID == "" {
			return errors.New("frontend event payload requires conversation_id")
		}
		if _, err := h.store.GetConversation(payload.ConversationID); err != nil {
			return err
		}
		scope = "frontend-events:" + payload.ConversationID
		frontendConversationID = payload.ConversationID
		mcpServers = h.frontendMCPServers
		agentID = h.frontendAgentID
	}
	runID := h.store.PersistentRun(scope)
	if runID == "" {
		var err error
		runID, err = h.client.CreatePersistentRun(ctx, agentID, agentfoundry.PersistentRunOptions{MCPServers: mcpServers, ClientKey: scope})
		if err != nil {
			return err
		}
		if err := h.checkFrontendConversation(ctx, frontendConversationID, scope, runID); err != nil {
			return err
		}
		if err := h.store.SetPersistentRun(scope, runID); err != nil {
			return err
		}
	}
	if err := h.checkFrontendConversation(ctx, frontendConversationID, scope, runID); err != nil {
		return err
	}
	message, err := json.Marshal(map[string]any{"source": event.Source, "category": event.Category, "payload": json.RawMessage(event.Payload), "hidden": true})
	if err != nil {
		return err
	}
	_, err = h.client.SendPersistentRunInputWithMetadata(ctx, runID, string(message), inputID, map[string]string{"source": event.Source})
	if agentfoundry.IsStaleRun(err) {
		runID, err = h.client.CreatePersistentRun(ctx, agentID, agentfoundry.PersistentRunOptions{MCPServers: mcpServers, ClientKey: scope})
		if err == nil {
			err = h.checkFrontendConversation(ctx, frontendConversationID, scope, runID)
		}
		if err == nil {
			err = h.store.SetPersistentRun(scope, runID)
		}
		if err == nil {
			err = h.checkFrontendConversation(ctx, frontendConversationID, scope, runID)
		}
		if err == nil {
			_, err = h.client.SendPersistentRunInputWithMetadata(ctx, runID, string(message), inputID, map[string]string{"source": event.Source})
		}
	}
	if err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		status, err := h.client.GetRunInputStatus(waitCtx, runID, inputID)
		if err != nil {
			return err
		}
		switch status {
		case "processed":
			return nil
		case "failed":
			slog.Warn("internal event agent input failed", "event", event.ID, "input", inputID, "run", runID)
			return errAgentInputFailed
		}
		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-ticker.C:
		}
	}
}

func (h *Handler) checkFrontendConversation(ctx context.Context, conversationID, scope, runID string) error {
	if conversationID == "" {
		return nil
	}
	if _, err := h.store.GetConversation(conversationID); err != nil {
		if runID != "" {
			if cancelErr := h.client.CancelRun(ctx, runID); cancelErr != nil && !agentfoundry.IsStaleRun(cancelErr) {
				slog.Warn("cancel orphan frontend event run", "run", runID, "error", cancelErr)
			}
			if h.store.PersistentRun(scope) == runID {
				if clearErr := h.store.SetPersistentRun(scope, ""); clearErr != nil {
					slog.Warn("clear orphan frontend event run", "run", runID, "error", clearErr)
				}
			}
		}
		return err
	}
	return nil
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
