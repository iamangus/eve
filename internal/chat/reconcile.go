package chat

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/io"
)

func (h *Handler) Reconcile(ctx context.Context) {
	const pollInterval = 2 * time.Second

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	h.reconcileOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.reconcileOnce(ctx)
		}
	}
}

const maxReconcileRetries = 3

func (h *Handler) reconcileOnce(ctx context.Context) {
	for _, id := range h.store.DeleteRequests() {
		if err := h.removeConversation(ctx, id); err != nil {
			slog.Warn("resume conversation deletion", "conversation", id, "error", err)
		}
	}
	runs, err := h.store.ActiveRuns()
	if err != nil {
		slog.Error("reconcile active runs", "error", err)
		return
	}
	for _, active := range runs {
		if err := h.reconcileRun(ctx, active.ConversationID, active.RunID); err != nil {
			slog.Warn("reconcile run", "conv", active.ConversationID, "run", active.RunID, "error", err)
		}
	}
	h.ioMgr.MigrateLegacyRuns(ctx)
	for _, convID := range h.store.PendingOwnerConversations() {
		if _, err := h.ioMgr.DrainPending(ctx, convID); err != nil {
			slog.Warn("start queued conversation input", "conv", convID, "error", err)
		}
	}
	h.ctxMgr.MaybeCompact(ctx)
}

func (h *Handler) reconcileRun(ctx context.Context, convID, runID string) error {
	pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rs, err := h.client.GetRun(pollCtx, runID)
	if err != nil {
		h.failedRuns[runID]++
		if h.failedRuns[runID] == maxReconcileRetries {
			slog.Warn("agentfoundry run temporarily unavailable", "conv", convID, "run", runID)
		}
		return err
	}
	delete(h.failedRuns, runID)
	if rs.Status == "failed" || rs.Status == "cancelled" || rs.Status == "canceled" || rs.Status == "error" || rs.Status == "unknown" {
		if err := h.store.RequeueSteersForRun(convID, runID); err != nil {
			return err
		}
	} else {
		for _, steer := range h.store.PendingSteersForRun(convID, runID) {
			if _, err := h.settleSteer(ctx, convID, steer); err != nil {
				return err
			}
		}
	}
	if rs.Persistent && rs.Status == "running" {
		msg, err := h.store.ActiveOwnerMessage(convID, runID)
		if err != nil {
			return err
		}
		if rs.CurrentInputID == fmt.Sprintf("chat-%s-%d", convID, msg.ID) {
			conv, err := h.store.GetConversation(convID)
			if err != nil {
				return err
			}
			if conv.ActiveRunID == runID && !conv.ActiveSubmitted {
				return h.retryActiveInput(ctx, convID, runID)
			}
		}
	}
	if rs.Persistent && (rs.Status == "waiting" || rs.Status == "completed") {
		msg, err := h.store.ActiveOwnerMessage(convID, runID)
		if err != nil {
			return err
		}
		expected := fmt.Sprintf("chat-%s-%d", convID, msg.ID)
		if rs.LastInputID != expected {
			if rs.Status == "completed" {
				if err := h.deliverTurn(ctx, convID, runID, "Error: run ended before processing this message"); err != nil {
					return err
				}
				return h.store.ClearActiveRunID(convID, runID)
			}
			return h.retryActiveInput(ctx, convID, runID)
		}
	}
	if rs.Status == "failed" || rs.Status == "cancelled" || rs.Status == "canceled" || rs.Status == "error" || rs.Status == "unknown" {
		message := rs.Error
		if message == "" {
			message = "agent run unavailable"
		}
		return h.finishTurn(ctx, convID, runID, "Error: "+message)
	}
	switch rs.Status {
	case "completed", "waiting":
		// Persistent AgentFoundry runs return to waiting after each completed
		// turn. For the visible frontend, that turn is complete even though the
		// underlying run remains available for future input.
		if rs.Response != "" || rs.Error != "" {
			response := rs.Response
			if rs.Error != "" {
				response = "Error: " + rs.Error
			}
			return h.finishTurn(ctx, convID, runID, response)
		}
		return h.store.ClearActiveRunID(convID, runID)
	default:
		return nil
	}
}

func (h *Handler) retryActiveInput(ctx context.Context, convID, runID string) error {
	err := h.ioMgr.RetryActiveInput(ctx, convID, runID)
	if agentfoundry.IsInvalidRequest(err) {
		active, lookupErr := h.store.ActiveRunForConversation(convID)
		if lookupErr != nil {
			return lookupErr
		}
		if active != "" {
			return h.finishTurn(ctx, convID, active, "Error: input rejected: "+err.Error())
		}
	}
	return err
}

func (h *Handler) finishTurn(ctx context.Context, convID, runID, response string) error {
	if !h.store.DeliveryReady(convID, runID) {
		return nil
	}
	if err := h.deliverTurn(ctx, convID, runID, response); err != nil {
		if recordErr := h.store.RecordDeliveryFailure(convID, runID, err.Error()); recordErr != nil {
			return recordErr
		}
		return err
	}
	return h.store.ClearActiveRunID(convID, runID)
}

func (h *Handler) deliverTurn(ctx context.Context, convID, runID, response string) error {
	channel, threadRef, replyTo, err := h.store.ActiveRunReply(convID, runID)
	if err != nil {
		channel = "web"
	}
	turnID := ""
	if msg, err := h.store.ActiveOwnerMessage(convID, runID); err == nil {
		turnID = fmt.Sprintf("chat-%s-%d", convID, msg.ID)
	}
	if channel == "web" {
		_, _, err := h.store.AppendAssistantForTurn(convID, runID, turnID, response, "web", "eve")
		return err
	}
	if err := h.ioMgr.Router.Reply(ctx, convID, response, io.ChannelType(channel), threadRef, replyTo, turnID); err != nil {
		slog.Warn("route reply", "conv", convID, "channel", channel, "error", err)
		return err
	}
	return nil
}
