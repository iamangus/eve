package chat

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/config"
	ctxmgr "github.com/iamangus/eve/internal/context"
	"github.com/iamangus/eve/internal/io"
	"github.com/iamangus/eve/internal/store"
	"github.com/iamangus/eve/internal/tasks"
)

type Handler struct {
	store   *store.Store
	client  *agentfoundry.Client
	ctxMgr  *ctxmgr.Manager
	ioMgr   *io.Manager
	agentID string
	mcpSrv  []agentfoundry.MCPServer
	tasks   *tasks.Manager

	// failedRuns tracks consecutive GetRun failures per run ID so the
	// Reconcile loop can retry transient agentfoundry errors instead of
	// abandoning a run (which would lose the response forever). Only touched
	// from the single Reconcile goroutine.
	failedRuns map[string]int
}

func NewHandler(store *store.Store, client *agentfoundry.Client, cfg config.Config, ctxMgr *ctxmgr.Manager, ioMgr *io.Manager) *Handler {
	// User-triggered runs get the chat MCP surface: task + calendar tools but
	// no send_message. Replies are delivered mechanically to the origin
	// channel; Eve never routes her own reply mid-conversation.
	mcpSrv := []agentfoundry.MCPServer{}
	if cfg.EVEMCPChatURL != "" {
		mcpSrv = append(mcpSrv, agentfoundry.MCPServer{
			Name:      "eve",
			URL:       cfg.EVEMCPChatURL,
			Transport: "streamable-http",
		})
	}
	return &Handler{
		store:      store,
		client:     client,
		ctxMgr:     ctxMgr,
		ioMgr:      ioMgr,
		agentID:    cfg.AssistantAgentID,
		mcpSrv:     mcpSrv,
		failedRuns: make(map[string]int),
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/conversations", h.listConversations)
	mux.HandleFunc("POST /api/conversations", h.createConversation)
	mux.HandleFunc("GET /api/conversations/{id}", h.getConversation)
	mux.HandleFunc("DELETE /api/conversations/{id}", h.deleteConversation)
	mux.HandleFunc("POST /api/conversations/{id}/messages", h.sendMessage)
	mux.HandleFunc("GET /runs/{id}/events", h.runEvents)
	mux.HandleFunc("GET /api/tasks", h.listTasks)
	mux.HandleFunc("POST /api/tasks/{id}/reply", h.replyTask)
	mux.HandleFunc("POST /api/tasks/{id}/cancel", h.cancelTask)
}

// SetTasks attaches the background-task manager so the task board can be
// injected into every assistant run and the task endpoints work.
func (h *Handler) SetTasks(tm *tasks.Manager) {
	h.tasks = tm
}

func (h *Handler) listConversations(w http.ResponseWriter, r *http.Request) {
	convs := h.store.ListConversations()
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
	conv, err := h.store.CreateConversation(req.Title)
	if err != nil {
		slog.Error("create conversation", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	writeJSON(w, http.StatusCreated, conv)
}

func (h *Handler) getConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	conv, err := h.store.GetConversation(id)
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
	if err := h.store.DeleteConversation(id); errors.Is(err, store.ErrNotFound) {
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

	h.ioMgr.Reg.Touch("web")

	prior, err := h.store.ConversationHistory(convID)
	if err != nil {
		slog.Error("load history", "conv", convID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	if prior == nil {
		prior = []store.Message{}
	}

	userCount, err := h.store.UserMessageCount(convID)
	if err != nil {
		slog.Error("count user messages", "conv", convID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}

	if err := h.store.AppendUserMessage(convID, req.Content, "web", "owner"); err != nil {
		slog.Error("append user message", "conv", convID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}

	if userCount == 0 {
		defaultTitle := req.Content
		if len(defaultTitle) > 40 {
			defaultTitle = strings.TrimSpace(defaultTitle[:40]) + "…"
		}
		_ = h.store.SetTitle(convID, defaultTitle)
	}

	history, _, renderErr := h.ctxMgr.RenderHistory(convID)
	if renderErr != nil {
		slog.Warn("render history", "conv", convID, "error", renderErr)
		history = make([]agentfoundry.Message, 0, len(prior))
		for _, m := range prior {
			history = append(history, agentfoundry.Message{Role: m.Role, Content: m.Content})
		}
	}

	history = prependTaskBoard(h.tasks, history)

	runID, err := h.client.RunAgentWith(r.Context(), h.agentID, agentfoundry.RunOptions{
		Message:    req.Content,
		History:    history,
		MCPServers: h.mcpSrv,
	})
	if err != nil {
		slog.Error("agentfoundry run", "conv", convID, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "agent run failed"})
		return
	}

	if err := h.store.SetActiveRun(convID, runID); err != nil {
		slog.Error("set active run", "conv", convID, "run", runID, "error", err)
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

// prependTaskBoard injects the current background-task state into the run so
// Eve always knows what is running, what needs input, and what just finished.
func prependTaskBoard(tm *tasks.Manager, history []agentfoundry.Message) []agentfoundry.Message {
	if tm == nil {
		return history
	}
	board := tm.ContextBlock()
	if board == "" {
		return history
	}
	ctxMsg := agentfoundry.Message{
		Role:    "user",
		Content: board,
	}
	out := make([]agentfoundry.Message, 0, len(history)+1)
	out = append(out, ctxMsg)
	out = append(out, history...)
	return out
}

func (h *Handler) listTasks(w http.ResponseWriter, r *http.Request) {
	if h.tasks == nil {
		writeJSON(w, http.StatusOK, []tasks.Task{})
		return
	}
	list := h.tasks.List()
	if list == nil {
		list = []tasks.Task{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Handler) replyTask(w http.ResponseWriter, r *http.Request) {
	if h.tasks == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tasks unavailable"})
		return
	}
	id := r.PathValue("id")
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Content) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content is required"})
		return
	}
	if err := h.tasks.Reply(r.Context(), id, req.Content); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (h *Handler) cancelTask(w http.ResponseWriter, r *http.Request) {
	if h.tasks == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tasks unavailable"})
		return
	}
	id := r.PathValue("id")
	if err := h.tasks.Cancel(r.Context(), id); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
