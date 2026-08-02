package trigger

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/email"
	"github.com/iamangus/eve/internal/store"
)

type Handler struct {
	store  *store.EmailStore
	client *agentfoundry.Client
	engine *Engine
}

func NewHandler(st *store.EmailStore, client *agentfoundry.Client, engine *Engine) *Handler {
	return &Handler{store: st, client: client, engine: engine}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/email/accounts", h.listAccounts)
	mux.HandleFunc("POST /api/email/accounts", h.createAccount)
	mux.HandleFunc("DELETE /api/email/accounts/{id}", h.deleteAccount)
	mux.HandleFunc("POST /api/email/accounts/{id}/test", h.testAccount)

	mux.HandleFunc("GET /api/triggers", h.listTriggers)
	mux.HandleFunc("POST /api/triggers", h.createTrigger)
	mux.HandleFunc("PUT /api/triggers/{id}", h.updateTrigger)
	mux.HandleFunc("DELETE /api/triggers/{id}", h.deleteTrigger)
	mux.HandleFunc("POST /api/triggers/{id}/test", h.testTrigger)

	mux.HandleFunc("GET /api/triggers/runs", h.listRuns)
}

type accountInput struct {
	Address  string `json:"address"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	UseTLS   bool   `json:"use_tls"`
	Enabled  bool   `json:"enabled"`
}

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	accounts := h.store.Accounts()
	out := make([]store.Account, 0, len(accounts))
	for _, a := range accounts {
		a.Password = ""
		out = append(out, a)
	}
	if out == nil {
		out = []store.Account{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) {
	var in accountInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	in.Address = strings.TrimSpace(in.Address)
	in.Host = strings.TrimSpace(in.Host)
	in.Username = strings.TrimSpace(in.Username)
	if in.Address == "" || in.Host == "" || in.Username == "" || in.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "address, host, username, and password are required"})
		return
	}
	if in.Port == 0 {
		if in.UseTLS {
			in.Port = 993
		} else {
			in.Port = 143
		}
	}
	acct, err := h.store.AddAccount(store.Account{
		Address:   in.Address,
		Host:      in.Host,
		Port:      in.Port,
		Username:  in.Username,
		Password:  in.Password,
		UseTLS:    in.UseTLS,
		Enabled:   true,
		CreatedAt: time.Now(),
	})
	if err != nil {
		slog.Error("create account", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	acct.Password = ""
	writeJSON(w, http.StatusCreated, acct)
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteAccount(id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		slog.Error("delete account", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) testAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	acct, err := h.store.GetAccount(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		slog.Error("get account", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	if err := email.Check(acct); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type triggerInput struct {
	AccountID string         `json:"account_id"`
	Name      string         `json:"name"`
	Filters   []store.Filter `json:"filters"`
	Prompt    string         `json:"prompt"`
	Enabled   bool           `json:"enabled"`
}

func (h *Handler) listTriggers(w http.ResponseWriter, r *http.Request) {
	triggers := h.store.Triggers()
	if triggers == nil {
		triggers = []store.Trigger{}
	}
	writeJSON(w, http.StatusOK, triggers)
}

func (h *Handler) createTrigger(w http.ResponseWriter, r *http.Request) {
	var in triggerInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	trigger, err := h.validateTriggerInput(in)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	trigger.ID = ""
	trigger.CreatedAt = time.Now()
	stored, err := h.store.AddTrigger(trigger)
	if err != nil {
		slog.Error("create trigger", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	writeJSON(w, http.StatusCreated, stored)
}

func (h *Handler) updateTrigger(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := h.store.GetTrigger(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		slog.Error("get trigger", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	var in triggerInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	trigger, err := h.validateTriggerInput(in)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	trigger.ID = existing.ID
	trigger.CreatedAt = existing.CreatedAt
	if err := h.store.UpdateTrigger(trigger); err != nil {
		slog.Error("update trigger", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	writeJSON(w, http.StatusOK, trigger)
}

func (h *Handler) validateTriggerInput(in triggerInput) (store.Trigger, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.AccountID == "" {
		return store.Trigger{}, errors.New("account_id is required")
	}
	if _, err := h.store.GetAccount(in.AccountID); err != nil {
		return store.Trigger{}, errors.New("account not found")
	}
	if in.Name == "" {
		return store.Trigger{}, errors.New("name is required")
	}
	if in.Prompt == "" {
		return store.Trigger{}, errors.New("prompt is required")
	}
	for _, f := range in.Filters {
		switch f.Field {
		case "sender", "recipient", "subject", "body":
		default:
			return store.Trigger{}, errors.New("invalid filter field: " + f.Field)
		}
		if strings.TrimSpace(f.Contains) == "" {
			return store.Trigger{}, errors.New("filter value is required")
		}
	}
	return store.Trigger{
		AccountID: in.AccountID,
		Name:      in.Name,
		Filters:   in.Filters,
		Prompt:    in.Prompt,
		Enabled:   in.Enabled,
	}, nil
}

func (h *Handler) deleteTrigger(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteTrigger(id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		slog.Error("delete trigger", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type testTriggerRequest struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func (h *Handler) testTrigger(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := h.store.GetTrigger(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		slog.Error("get trigger", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	var in testTriggerRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	msg := store.EmailMessage{From: in.From, To: in.To, Subject: in.Subject, Body: in.Body}
	matched := Matches(t, msg)
	var matchedFilters []string
	if matched {
		for _, f := range t.Filters {
			if filterMatches(f, msg) {
				matchedFilters = append(matchedFilters, f.Field+" contains "+f.Contains)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"matched":         matched,
		"matched_filters": matchedFilters,
		"prompt": func() string {
			if matched {
				return ComposePrompt(t, msg)
			}
			return ""
		}(),
	})
}

type runView struct {
	store.TriggerRun
	TriggerName    string `json:"trigger_name"`
	AccountAddress string `json:"account_address"`
}

func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	var runs []store.TriggerRun
	if id := r.URL.Query().Get("trigger_id"); id != "" {
		runs = h.store.RunsForTrigger(id)
	} else {
		runs = h.store.Runs()
	}
	limit := 50
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = n
		}
	}
	if len(runs) > limit {
		runs = runs[:limit]
	}

	accounts := make(map[string]string)
	for _, a := range h.store.Accounts() {
		accounts[a.ID] = a.Address
	}
	triggers := make(map[string]string)
	for _, t := range h.store.Triggers() {
		triggers[t.ID] = t.Name
	}

	out := make([]runView, 0, len(runs))
	for _, run := range runs {
		out = append(out, runView{
			TriggerRun:     run,
			TriggerName:    triggers[run.TriggerID],
			AccountAddress: accounts[run.AccountID],
		})
	}
	if out == nil {
		out = []runView{}
	}
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
