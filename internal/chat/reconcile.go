package chat

import (
	"context"
	"log/slog"
	"time"

	"github.com/iamangus/eve/internal/store"
)

// Reconcile periodically polls agentfoundry for conversations that have an
// in-flight active_run_id (e.g. after a BFF crash mid-stream) and finalizes
// them: persists the assistant text if the run completed, or clears the
// active_run_id if the run is gone/errored.
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
	runs, err := store.ActiveRuns(h.db.DB)
	if err != nil {
		slog.Error("reconcile active runs", "error", err)
		return
	}
	for convID, runID := range runs {
		if err := h.reconcileRun(ctx, convID, runID); err != nil {
			slog.Warn("reconcile run", "conv", convID, "run", runID, "error", err)
		}
	}
}

func (h *Handler) reconcileRun(ctx context.Context, convID, runID string) error {
	pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rs, err := h.client.GetRun(pollCtx, runID)
	if err != nil {
		_ = store.ClearActiveRun(h.db.DB, convID)
		return err
	}
	switch rs.Status {
	case "completed":
		if rs.Response != "" {
			if err := store.AppendAssistantMessage(h.db.DB, convID, runID, rs.Response); err != nil {
				return err
			}
		}
		return store.ClearActiveRun(h.db.DB, convID)
	case "failed", "cancelled", "canceled", "error", "unknown":
		return store.ClearActiveRun(h.db.DB, convID)
	default:
		return nil
	}
}