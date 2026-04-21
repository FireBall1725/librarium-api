// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package workers

import (
	"context"
	"errors"
	"log/slog"
	"time"

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

// Timeout raises the per-job ceiling well above River's 60s default. Ollama
// with a 32B thinking model (qwen3, deepseek-r1) can legitimately take 5-10
// minutes on modest hardware — the provider's own 10m HTTP client timeout
// and watchCancellation (driven by DB status polling) are the real guards.
func (w *AISuggestionsWorker) Timeout(*river.Job[models.AISuggestionsJobArgs]) time.Duration {
	return 20 * time.Minute
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
		// Suggestions runs aren't safe to auto-retry: the common failures
		// (context-length exceeded, provider 4xx, invalid JSON from the model)
		// are deterministic — the second attempt will fail the same way, just
		// spending tokens and cluttering history. Transient failures
		// (network blip, provider timeout) are better handled by the user
		// hitting "Run now" again or waiting for the next scheduled pass.
		// River's default is 25 retries with backoff; JobCancel stops that.
		slog.Warn("ai suggestions failed — cancelling further attempts", "user_id", args.UserID, "error", err)
		return river.JobCancel(err)
	}
	return nil
}
