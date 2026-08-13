package io

import "net/http"

// WebhookConfig guards the inbound webhook endpoints for input-only channels.
// SMS and voice have no output adapter in v1: they exercise the fallback
// router, which routes replies/notifications to whichever output channel is
// reachable (the owner cannot receive Eve's answer on a speakerless device).
type WebhookConfig struct {
	SMSToken  string
	VoiceToken string
}

func (c WebhookConfig) smsEnabled() bool   { return c.SMSToken != "" }
func (c WebhookConfig) voiceEnabled() bool { return c.VoiceToken != "" }

// RegisterWebhooks registers the input-only SMS and voice channels plus their
// webhook endpoints. Called by main when the respective tokens are set.
func (m *Manager) RegisterWebhooks(mux *http.ServeMux, cfg WebhookConfig) {
	if cfg.smsEnabled() {
		m.Reg.Register(Channel{
			ID:         "sms",
			Type:       ChannelSMS,
			Name:       "SMS",
			Input:      true,
			Output:     false,
			Streams:    false,
			RichText:   false,
			Preference: 20,
		})
		mux.HandleFunc("POST /api/inbound/sms", m.webhookInbound(ChannelSMS, cfg.SMSToken))
	}
	if cfg.voiceEnabled() {
		m.Reg.Register(Channel{
			ID:         "voice",
			Type:       ChannelVoice,
			Name:       "Voice device",
			Input:      true,
			Output:     false,
			Streams:    false,
			RichText:   false,
			Preference: 10,
		})
		mux.HandleFunc("POST /api/inbound/voice", m.webhookInbound(ChannelVoice, cfg.VoiceToken))
	}
}

// webhookInbound is the shared handler for input-only channel webhooks. It
// accepts a token (query param or Authorization header) matching the channel's
// token, then funnels the payload into the manager's Inbound path.
func (m *Manager) webhookInbound(ch ChannelType, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tokenOK(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad token"})
			return
		}
		var body struct {
			Sender    string `json:"sender"`
			Text      string `json:"text"`
			Transcript string `json:"transcript"` // voice STT payload
		}
		if err := decodeJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		text := body.Text
		if text == "" {
			text = body.Transcript
		}
		if text == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text or transcript is required"})
			return
		}
		if body.Sender == "" {
			body.Sender = m.Ident.Owner().Name
		}
		if _, err := m.Inbound(r.Context(), InboundMessage{
			Channel: ch,
			Sender:  body.Sender,
			Text:    text,
		}); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
	}
}

func tokenOK(r *http.Request, expected string) bool {
	if expected == "" {
		return false
	}
	if t := r.URL.Query().Get("token"); t != "" {
		return t == expected
	}
	return r.Header.Get("Authorization") == "Bearer "+expected
}
