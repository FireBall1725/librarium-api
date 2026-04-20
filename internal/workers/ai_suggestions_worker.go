// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package workers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/riverqueue/river"
)

// AISuggestionsWorker runs the suggestions pipeline for one user per job.
// Longer timeout than the default — a multi-pass AI run with enrichment can
// legitimately take a couple minutes.
type AISuggestionsWorker struct {
	river.WorkerDefaults[models.AISuggestionsJobArgs]
	svc *service.SuggestionsService
}

func NewAISuggestionsWorker(svc *service.SuggestionsService) *AISuggestionsWorker {
	return &AISuggestionsWorker{svc: svc}
}

func (w *AISuggestionsWorker) Work(ctx context.Context, job *river.Job[models.AISuggestionsJobArgs]) error {
	args := job.Args
	slog.Info("ai suggestions job started", "user_id", args.UserID, "triggered_by", args.TriggeredBy)
	_, err := w.svc.RunForUser(ctx, args.UserID, args.TriggeredBy)
	switch {
	case errors.Is(err, service.ErrAIDisabled):
		// Soft skip — don't retry. Nothing to do for this user right now.
		slog.Info("ai suggestions skipped", "user_id", args.UserID, "reason", "disabled")
		return nil
	case errors.Is(err, service.ErrRateLimited):
		slog.Info("ai suggestions skipped", "user_id", args.UserID, "reason", "rate_limited")
		return nil
	case errors.Is(err, service.ErrAlreadyRunning):
		// Another run is already in flight for this user — skip without retry.
		slog.Info("ai suggestions skipped", "user_id", args.UserID, "reason", "already_running")
		return nil
	case errors.Is(err, service.ErrRunCancelled):
		// Admin cancelled — the run row is already marked cancelled. No retry.
		slog.Info("ai suggestions cancelled", "user_id", args.UserID)
		return nil
	case err != nil:
		return err
	}
	return nil
}
