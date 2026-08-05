package chat

import (
	"context"
	"log/slog"
	"time"
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

func (h *Handler) reconcileOnce(ctx context.Context) {
	runs, err := h.store.ActiveRuns()
	if err != nil {
		slog.Error("reconcile active runs", "error", err)
		return
	}
	for convID, runID := range runs {
		if err := h.reconcileRun(ctx, convID, runID); err != nil {
			slog.Warn("reconcile run", "conv", convID, "run", runID, "error", err)
		}
	}
	h.ctxMgr.MaybeCompact(ctx)
}

func (h *Handler) reconcileRun(ctx context.Context, convID, runID string) error {
	pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rs, err := h.client.GetRun(pollCtx, runID)
	if err != nil {
		_ = h.store.ClearActiveRun(convID)
		return err
	}
	switch rs.Status {
	case "completed":
		if rs.Response != "" {
			if err := h.store.AppendAssistantMessage(convID, runID, rs.Response); err != nil {
				return err
			}
		}
		return h.store.ClearActiveRun(convID)
	case "failed", "cancelled", "canceled", "error", "unknown":
		return h.store.ClearActiveRun(convID)
	default:
		return nil
	}
}