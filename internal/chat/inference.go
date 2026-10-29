package chat

import (
	"net/http"
	"strconv"
)

func (h *Handler) messageInference(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("message"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid message"})
		return
	}
	conv, err := h.store.GetConversation(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "conversation not found"})
		return
	}
	for _, msg := range conv.Messages {
		if msg.ID != id || msg.Role != "assistant" {
			continue
		}
		if msg.RunID == "" || msg.TurnID == "" {
			writeJSON(w, http.StatusOK, map[string]any{"attempts": []any{}})
			return
		}
		body, err := h.client.RunInference(r.Context(), msg.RunID, msg.TurnID, r.URL.Query().Get("cursor"))
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Inference metrics are unavailable."})
			return
		}
		writeJSON(w, http.StatusOK, body)
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "message not found"})
}

func (h *Handler) providerInference(w http.ResponseWriter, r *http.Request) {
	body, err := h.client.ProviderInference(r.Context(), r.URL.Query().Get("provider_id"))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Provider metrics are unavailable."})
		return
	}
	writeJSON(w, http.StatusOK, body)
}
