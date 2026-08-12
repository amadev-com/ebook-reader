package cli

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ebook-reader/internal/project"
	"ebook-reader/internal/translation"
)

// pollBatchUntilTerminal polls the batch status every pollInt seconds until a
// terminal status is reached. It updates and persists the batch state on each
// poll. The label is used in log messages to distinguish batch types (e.g.
// "batch", "merge batch"). Returns the batch client for downstream result
// processing.
func pollBatchUntilTerminal(
	ctx context.Context,
	proj *project.Project,
	state *translation.BatchState,
	pollInt int,
	label string,
) (*translation.BatchClient, error) {
	batchClient, err := translation.NewBatchClient(proj.Cfg.OpenAI.BaseURL, proj.Cfg.OpenAI.MaxRetries)
	if err != nil {
		return nil, err
	}

	var info translation.BatchStatusInfo
	for {
		if ctx.Err() != nil {
			slog.Default().
				InfoContext(ctx, "interrupted by signal", "batch_id", state.BatchID, "last_status", state.Status)
			return nil, ctx.Err()
		}

		if info, err = batchClient.PollBatch(ctx, state.BatchID); err != nil {
			return nil, fmt.Errorf("poll %s: %w", label, err)
		}

		state.Status = info.Status
		state.OutputFileID = info.OutputFileID
		state.ErrorFileID = info.ErrorFileID
		state.Total = info.Total
		state.Completed = info.Completed
		state.Failed = info.Failed
		if err = translation.SaveBatchState(proj.AIDir(), state); err != nil {
			return nil, fmt.Errorf("save %s state: %w", label, err)
		}

		slog.Default().InfoContext(ctx, label+" status",
			"batch_id", state.BatchID, "status", info.Status,
			"completed", info.Completed, "failed", info.Failed, "total", info.Total)

		if translation.IsTerminalStatus(info.Status) {
			break
		}

		slog.Default().InfoContext(ctx, "waiting for "+label, "poll_seconds", pollInt)
		select {
		case <-ctx.Done():
			slog.Default().InfoContext(ctx, "interrupted during poll wait", "batch_id", state.BatchID)
			return nil, ctx.Err()
		case <-time.After(time.Duration(pollInt) * time.Second):
		}
	}

	return batchClient, nil
}

// batchTerminalError returns an error describing the terminal status of the
// batch, or nil if the batch completed successfully.
func batchTerminalError(state *translation.BatchState, label string) error {
	switch state.Status {
	case translation.BatchStatusFailed:
		return fmt.Errorf("%s %s failed — check OpenAI dashboard for details", label, state.BatchID)
	case translation.BatchStatusExpired:
		return fmt.Errorf("%s %s expired before completion", label, state.BatchID)
	case translation.BatchStatusCancelled:
		return fmt.Errorf("%s %s was cancelled", label, state.BatchID)
	default:
		return fmt.Errorf("%s %s ended in unexpected status: %s", label, state.BatchID, state.Status)
	}
}
