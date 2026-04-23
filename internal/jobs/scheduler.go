// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package jobs

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// Scheduler walks job_schedules on a short tick, parses each row's cron
// expression, and fires the kind's Enqueue hook when the next scheduled
// time has passed since last_fired_at. Replaces the previous hardcoded
// AI-suggestions ticker — new kinds drop in by registering a Definition
// with an Enqueue hook.
type Scheduler struct {
	registry *Registry
	jobs     *repository.JobRepo
	parser   cron.Parser
}

// NewScheduler wires up a scheduler around the given registry + repo.
// Uses the standard 5-field cron parser (minute / hour / day-of-month /
// month / day-of-week) — `react-js-cron` on the web emits the same.
func NewScheduler(registry *Registry, jr *repository.JobRepo) *Scheduler {
	return &Scheduler{
		registry: registry,
		jobs:     jr,
		// NB: default parser doesn't support seconds. Keep it 5-field to
		// match the UI editor; if we ever want second-level granularity
		// we'll swap to cron.Descriptor explicitly.
		parser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
}

// Run blocks until ctx is cancelled. Fires a tick every 30 seconds —
// fine-grained enough that a "once a minute" cron misses at most one
// minute, and infrequent enough that the DB query is trivial.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	// First tick after 10s so fresh startups don't sit idle waiting on
	// the next 30s boundary.
	first := time.NewTimer(10 * time.Second)
	defer first.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			s.tick(ctx)
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick walks every schedule row once. Per-schedule errors are logged but
// don't stop the loop — one bad kind shouldn't starve others.
func (s *Scheduler) tick(ctx context.Context) {
	schedules, err := s.jobs.ListSchedules(ctx)
	if err != nil {
		slog.Warn("scheduler: list schedules failed", "error", err)
		return
	}
	now := time.Now()
	for _, sched := range schedules {
		if !sched.Enabled {
			continue
		}
		def := s.registry.Get(Kind(sched.Kind))
		if def == nil || def.Enqueue == nil {
			// Kind isn't wired up for cron firing (yet). Leave the
			// schedule row alone — admin can still edit it; we just
			// won't fire until a worker registers an Enqueue.
			continue
		}

		schedule, err := s.parser.Parse(sched.Cron)
		if err != nil {
			slog.Warn("scheduler: invalid cron", "kind", sched.Kind, "cron", sched.Cron, "error", err)
			continue
		}

		// Reference time for "what's the next fire relative to a known
		// previous fire". For fresh schedules with no last_fired_at we
		// use created_at so a never-fired-before schedule fires on the
		// first tick that's past its first cron time.
		prev := sched.CreatedAt
		if sched.LastFiredAt != nil {
			prev = *sched.LastFiredAt
		}
		next := schedule.Next(prev)
		if next.After(now) {
			continue
		}

		if err := s.fire(ctx, sched, def); err != nil {
			slog.Warn("scheduler: enqueue failed", "kind", sched.Kind, "error", err)
			// Don't stamp last_fired_at — let the next tick retry.
			continue
		}
		if err := s.jobs.MarkScheduleFired(ctx, sched.ID); err != nil {
			slog.Warn("scheduler: mark fired failed", "kind", sched.Kind, "error", err)
		}
	}
}

// fire creates an umbrella jobs row for the scheduled trigger and hands
// off to the kind's Enqueue hook. The umbrella row is the record of
// "the scheduler ticked this kind"; Enqueue is free to create further
// per-work umbrella rows (e.g. AI suggestions fans out per-user) or
// treat the scheduler's row as the work itself (e.g. cover backfill
// runs once against the catalog).
//
// After Enqueue returns, this scheduler-tick row transitions to
// completed or failed. Per-work rows (if any) manage their own
// lifecycle independently.
func (s *Scheduler) fire(ctx context.Context, sched *models.JobSchedule, def *Definition) error {
	now := time.Now()
	j := &models.Job{
		Kind:        string(sched.Kind),
		Status:      models.JobStatusRunning,
		TriggeredBy: models.JobTriggeredByScheduler,
		ScheduleID:  &sched.ID,
		StartedAt:   &now,
	}
	if err := s.jobs.CreateJob(ctx, j); err != nil {
		return err
	}
	cfg := sched.Config
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	if err := def.Enqueue(ctx, TriggerCtx{
		JobID:       j.ID,
		TriggeredBy: models.JobTriggeredByScheduler,
		ScheduleID:  &sched.ID,
	}, cfg); err != nil {
		_ = s.jobs.MarkFinished(ctx, j.ID, models.JobStatusFailed, err.Error())
		return err
	}
	return s.jobs.MarkFinished(ctx, j.ID, models.JobStatusCompleted, "")
}

// ensure uuid is imported for nil checks elsewhere that compile by
// referencing this package first — no-op body.
var _ = uuid.Nil
