package chat

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/config"
	"github.com/iamangus/eve/internal/store"
)

type Handler struct {
	db       *store.DB
	client   *agentfoundry.Client
	agentID  string
	titleID  string
}

func NewHandler(db *store.DB, client *agentfoundry.Client, cfg config.Config) *Handler {
	return &Handler{
		db:      db,
		client:  client,
		agentID: cfg.AssistantAgentID,
		titleID: cfg.TitleAgentID,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/conversations", h.listConversations)
	mux.HandleFunc("POST /api/conversations", h.createConversation)
	mux.HandleFunc("GET /api/conversations/{id}", h.getConversation)
	mux.HandleFunc("DELETE /api/conversations/{id}", h.deleteConversation)
	mux.HandleFunc("POST /api/conversations/{id}/messages", h.sendMessage)
	mux.HandleFunc("GET /runs/{id}/events", h.runEvents)
}

func (h *Handler) listConversations(w http.ResponseWriter, r *http.Request) {
	convs, err := store.ListConversations(h.db.DB)
	if err != nil {
		slog.Error("list conversations", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	if convs == nil {
		convs = []store.ConversationSummary{}
	}
	writeJSON(w, http.StatusOK, convs)
}

func (h *Handler) createConversation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	conv, err := store.CreateConversation(h.db.DB, req.Title)
	if err != nil {
		slog.Error("create conversation", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	writeJSON(w, http.StatusCreated, conv)
}

func (h *Handler) getConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	conv, err := store.GetConversation(h.db.DB, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		slog.Error("get conversation", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	if conv.Messages == nil {
		conv.Messages = []store.Message{}
	}
	writeJSON(w, http.StatusOK, conv)
}

func (h *Handler) deleteConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := store.DeleteConversation(h.db.DB, id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		slog.Error("delete conversation", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type sendMessageRequest struct {
	Content string `json:"content"`
}

type sendMessageResponse struct {
	RunID string `json:"run_id"`
}

func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")

	var req sendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content is required"})
		return
	}

	prior, err := store.ConversationHistory(h.db.DB, convID)
	if err != nil {
		slog.Error("load history", "conv", convID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	if prior == nil {
		prior = []store.Message{}
	}

	userCount, err := store.UserMessageCount(h.db.DB, convID)
	if err != nil {
		slog.Error("count user messages", "conv", convID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}

	if err := store.AppendUserMessage(h.db.DB, convID, req.Content); err != nil {
		slog.Error("append user message", "conv", convID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}

	if userCount == 0 {
		defaultTitle := req.Content
		if len(defaultTitle) > 40 {
			defaultTitle = strings.TrimSpace(defaultTitle[:40]) + "…"
		}
		_ = store.SetTitle(h.db.DB, convID, defaultTitle)
	}

	history := make([]agentfoundry.Message, 0, len(prior))
	for _, m := range prior {
		history = append(history, agentfoundry.Message{Role: m.Role, Content: m.Content})
	}

	runID, err := h.client.RunAgent(r.Context(), h.agentID, req.Content, history)
	if err != nil {
		slog.Error("agentfoundry run", "conv", convID, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "agent run failed"})
		return
	}

	if err := store.SetActiveRun(h.db.DB, convID, runID); err != nil {
		slog.Error("set active run", "conv", convID, "run", runID, "error", err)
	}

	if userCount == 0 && h.titleID != "" {
		go h.generateTitle(convID, req.Content)
	}

	writeJSON(w, http.StatusAccepted, sendMessageResponse{RunID: runID})
}

func (h *Handler) runEvents(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")

	body, err := h.client.StreamRunEvents(r.Context(), runID)
	if err != nil {
		slog.Error("open agentfoundry SSE", "run", runID, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "stream error"})
		return
	}
	defer body.Close()

	// The proxy is a pure byte forwarder. Persistence of the assistant message
	// and clearing active_run_id is handled solely by the Reconcile loop (the
	// single writer to messages + active_run_id), avoiding a race where both the
	// SSE done path and a reconcile poll would persist the same assistant msg.
	proxySSE(w, body, func(ev SSEEvent) {})
}

func (h *Handler) generateTitle(convID, firstMessage string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prompt := "Generate a concise title (at most 6 words) for a conversation that starts with the following user message. Reply with only the title text, no quotes, no punctuation.\n\nUser message:\n" + firstMessage
	runID, err := h.client.RunAgent(ctx, h.titleID, prompt, nil)
	if err != nil {
		slog.Warn("title run", "conv", convID, "error", err)
		return
	}
	text, err := h.client.AwaitRunText(ctx, runID, 25*time.Second)
	if err != nil {
		slog.Warn("title await", "conv", convID, "run", runID, "error", err)
		return
	}
	title := cleanTitle(text)
	if title == "" {
		return
	}
	if err := store.SetTitle(h.db.DB, convID, title); err != nil {
		slog.Error("set title", "conv", convID, "error", err)
		return
	}
	slog.Info("title generated", "conv", convID, "title", title)
}

func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'`")
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		s = strings.TrimSpace(s[:80]) + "…"
	}
	return s
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}