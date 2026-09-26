package chat

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/iamangus/eve/internal/agentfoundry"
	ctxmgr "github.com/iamangus/eve/internal/context"
	"github.com/iamangus/eve/internal/io"
	"github.com/iamangus/eve/internal/store"
	"github.com/iamangus/eve/internal/tasks"
)

type Handler struct {
	store  *store.Store
	client *agentfoundry.Client
	ctxMgr *ctxmgr.Manager
	ioMgr  *io.Manager
	tasks  *tasks.Manager

	// failedRuns tracks consecutive GetRun failures per run ID so the
	// Reconcile loop can retry transient agentfoundry errors instead of
	// abandoning a run (which would lose the response forever). Only touched
	// from the single Reconcile goroutine.
	failedRuns map[string]int
}

func NewHandler(store *store.Store, client *agentfoundry.Client, ctxMgr *ctxmgr.Manager, ioMgr *io.Manager) *Handler {
	return &Handler{
		store:      store,
		client:     client,
		ctxMgr:     ctxMgr,
		ioMgr:      ioMgr,
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
	if errors.Is(err, store.ErrDeleteInProgress) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "conversation is being deleted"})
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
	activeRunIDs, err := h.store.BeginConversationDelete(id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		if errors.Is(err, store.ErrDeleteInProgress) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		}
		return
	}
	if err == nil {
		defer h.store.EndConversationDelete(id)
	}
	scopes := []string{"frontend-v2:" + id, "frontend-events:" + id, "frontend:" + id}
	runIDs := make(map[string]struct{})
	for _, runID := range activeRunIDs {
		runIDs[runID] = struct{}{}
	}
	for _, scope := range scopes {
		if runID := h.store.PersistentRun(scope); runID != "" {
			runIDs[runID] = struct{}{}
		}
	}
	for runID := range runIDs {
		if err := h.client.CancelRun(r.Context(), runID); err != nil && !agentfoundry.IsStaleRun(err) {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to stop conversation run"})
			return
		}
	}
	for _, scope := range scopes {
		if h.store.PersistentRun(scope) != "" {
			if err := h.store.SetPersistentRun(scope, ""); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to clear conversation run"})
				return
			}
		}
	}
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
	Content  string `json:"content"`
	ClientID string `json:"client_id,omitempty"`
}

type sendMessageResponse struct {
	RunID     string `json:"run_id,omitempty"`
	MessageID int64  `json:"message_id"`
	Queued    bool   `json:"queued"`
	Steered   bool   `json:"steered"`
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
	if len(req.ClientID) > 256 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "client_id is too long"})
		return
	}

	h.ioMgr.Reg.Touch("web")

	userCount, err := h.store.UserMessageCount(convID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		slog.Error("count user messages", "conv", convID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}

	steerRun := h.store.SteerTarget(convID)

	var appended *store.Message
	var steerTarget string
	if steerRun != "" {
		appended, _, steerTarget, err = h.store.AppendSteeredOwnerMessageWithSource(convID, req.Content, "web", "owner", "", "", req.ClientID, steerRun)
	} else {
		appended, _, err = h.store.AppendQueuedOwnerMessageWithSource(convID, req.Content, "web", "owner", "", "", req.ClientID)
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if errors.Is(err, store.ErrSourceConflict) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
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

	if steerTarget != "" {
		if h.store.ConfirmedSteer(convID, steerTarget, appended.ID) {
			writeJSON(w, http.StatusAccepted, sendMessageResponse{RunID: steerTarget, MessageID: appended.ID, Steered: true})
			return
		}
		steer, pending := h.store.PendingSteer(convID, steerTarget, appended.ID)
		if !pending {
			writeJSON(w, http.StatusAccepted, sendMessageResponse{MessageID: appended.ID, Queued: true})
			return
		}
		settled, steerErr := h.settleSteer(r.Context(), convID, steer)
		if steerErr != nil {
			slog.Warn("steering confirmation pending", "conv", convID, "run", steerTarget, "message", appended.ID, "error", steerErr)
			writeJSON(w, http.StatusAccepted, sendMessageResponse{MessageID: appended.ID, Queued: true})
			return
		}
		if settled {
			writeJSON(w, http.StatusAccepted, sendMessageResponse{RunID: steerTarget, MessageID: appended.ID, Steered: true})
			return
		}
	}

	runID, err := h.ioMgr.DrainPending(r.Context(), convID)
	if err != nil {
		slog.Warn("queued web input for retry", "conv", convID, "message", appended.ID, "error", err)
		runID = ""
	}
	writeJSON(w, http.StatusAccepted, sendMessageResponse{RunID: runID, MessageID: appended.ID, Queued: runID == ""})
}

func (h *Handler) settleSteer(ctx context.Context, convID string, steer store.SteeringInput) (bool, error) {
	message, active := h.store.SteeringMessage(convID, steer)
	if message == nil {
		return false, store.ErrNotFound
	}
	if !active {
		return false, h.store.QueueSteer(convID, steer.RunID, steer.MessageID)
	}
	if err := h.ioMgr.SteerActive(ctx, convID, steer.RunID, message); err != nil {
		var httpErr *agentfoundry.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusConflict {
			var reason struct {
				Code string `json:"code"`
			}
			if json.Unmarshal([]byte(httpErr.Body), &reason) == nil && reason.Code == "input_failed" {
				return true, h.store.ConfirmSteer(convID, steer.RunID, steer.MessageID)
			}
		}
		if errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusConflict || httpErr.StatusCode == http.StatusNotFound) {
			return false, h.store.QueueSteer(convID, steer.RunID, steer.MessageID)
		}
		return false, err
	}
	if err := h.store.ConfirmSteer(convID, steer.RunID, steer.MessageID); err != nil {
		return false, err
	}
	return true, nil
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
