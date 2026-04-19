// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

// Package background contains long-running background workers.
package background

import (
	"context"
	"log/slog"
	"time"

	"github.com/fireball1725/librarium-api/internal/service"
)

// ReleaseChecker periodically syncs series volume data from external providers.
type ReleaseChecker struct {
	svc      *service.ReleaseSyncService
	interval time.Duration
}

func NewReleaseChecker(svc *service.ReleaseSyncService, interval time.Duration) *ReleaseChecker {
	return &ReleaseChecker{svc: svc, interval: interval}
}

// Start runs the release checker until ctx is cancelled. Call as a goroutine.
func (c *ReleaseChecker) Start(ctx context.Context) {
	slog.Info("release checker started", "interval", c.interval)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("release checker stopped")
			return
		case <-ticker.C:
			slog.Info("release checker: running sync")
			c.svc.SyncAll(ctx)
			slog.Info("release checker: sync complete")
		}
	}
}
