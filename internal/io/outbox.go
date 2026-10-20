package io

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/iamangus/eve/internal/events"
	"github.com/iamangus/eve/internal/store"
)

type notification struct {
	ConversationID string `json:"conversation_id"`
	Content        string `json:"content"`
	Purpose        string `json:"purpose"`
	Channel        string `json:"channel,omitempty"`
}

func (m *Manager) EnqueueNotification(id, convID, content, purpose, channel string) (string, error) {
	if strings.TrimSpace(content) == "" || len(content) > 1<<20 || len(id) > 512 {
		return "", fmt.Errorf("notification content or delivery ID is invalid")
	}
	if purpose == "" {
		purpose = PurposeNotification
	}
	switch purpose {
	case PurposeNotification, PurposeQuestion, PurposeReminder:
	default:
		return "", fmt.Errorf("invalid notification purpose")
	}
	if channel != "" {
		if endpoint, ok := m.Reg.Get(channel); !ok || !endpoint.Output {
			return "", fmt.Errorf("channel is not an output endpoint")
		}
	}
	if !m.Router.proactiveEnabled {
		return "", ErrDisabled
	}
	if convID == "" {
		convID = m.store.PrimaryConversationID()
	}
	if _, err := m.store.GetConversation(convID); err != nil {
		return "", err
	}
	if id == "" {
		id = events.NewID()
	}
	data, err := json.Marshal(notification{convID, content, purpose, channel})
	if err != nil {
		return "", err
	}
	_, err = m.Outbox.Enqueue("notification", "outbound", id, data)
	return id, err
}

func (m *Manager) RunOutbox(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := m.deliverNotification(ctx); err != nil {
			slog.Warn("outbound delivery", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Manager) deliverNotification(ctx context.Context) error {
	if time.Now().Before(m.nextNotification) {
		return nil
	}
	event, err := m.Outbox.Lease(time.Now().UTC(), 2*time.Minute)
	if err != nil || event == nil {
		return err
	}
	var msg notification
	if err := json.Unmarshal(event.Payload, &msg); err != nil {
		return m.Outbox.Fail(event, err.Error())
	}
	if _, err := m.store.GetConversation(msg.ConversationID); errors.Is(err, store.ErrNotFound) {
		return m.Outbox.Fail(event, "conversation deleted")
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := m.Router.NotifyForDelivery(deliveryCtx, msg.ConversationID, msg.Content, msg.Purpose, msg.Channel, "notification-"+event.ID); err != nil {
		return errors.Join(err, m.Outbox.RetryError(event, err.Error()))
	}
	if err := m.Outbox.Complete(event); err != nil {
		return err
	}
	m.nextNotification = time.Now().Add(m.cooldown)
	return nil
}

func (m *Manager) deliveryStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, m.Outbox.Snapshot())
}
