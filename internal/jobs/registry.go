// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

// Package jobs holds the kind registry and runtime glue that bridges the
// unified jobs umbrella (jobs / job_events / job_schedules tables) to
// River-backed workers. Each job kind (import, enrichment, AI suggestions,
// etc.) registers a Definition here that tells the scheduler how to turn
// a schedule row + trigger into a River enqueue.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
)

// Kind is the stable string identifier for a job kind. Matches
// jobs.kind, job_schedules.kind, and the Definition registered below.
type Kind string

// Built-in kinds. New kinds can declare their own constants alongside
// their worker package and register from there.
const (
	KindImport         Kind = "import"
	KindEnrichment     Kind = "enrichment"
	KindAISuggestions  Kind = "ai_suggestions"
	KindCoverBackfill  Kind = "cover_backfill"
)

// TriggerCtx is everything the Enqueue hook of a Definition needs
// beyond the kind-specific config JSON. ScheduleID is set when the
// trigger came from the cron scheduler; nil otherwise.
type TriggerCtx struct {
	JobID       uuid.UUID
	TriggeredBy models.JobTriggeredBy
	CreatedBy   *uuid.UUID
	ScheduleID  *uuid.UUID
}

// EnqueueFn is the bridge from the framework to the kind's worker. It
// receives the umbrella jobs row that's already been created + the
// config JSON from either the schedule row or the inline admin/user
// request. Implementations hand off to the River client registered
// at wire-time.
type EnqueueFn func(ctx context.Context, trig TriggerCtx, config json.RawMessage) error

// Definition is the per-kind registration. Workers fill this in at
// startup; the scheduler and admin endpoints read from the registry to
// figure out what's available and how to fire it.
type Definition struct {
	Kind        Kind
	DisplayName string
	Description string
	// Schedulable means it can appear in the job_schedules table with
	// a cron expression. One-off queued jobs (import/enrichment) may
	// be un-schedulable.
	Schedulable bool
	// DefaultCron is the seed value when an admin first enables a
	// schedule for this kind from the UI.
	DefaultCron string
	// Enqueue turns a trigger into a River job. Called by the
	// scheduler loop (for scheduled runs) or directly by admin /
	// user-facing endpoints (for manual runs).
	Enqueue EnqueueFn
}

// Registry is the runtime map of Kind → Definition. Safe for concurrent
// reads after wire-up; Register is expected to only be called from main
// during startup.
type Registry struct {
	mu   sync.RWMutex
	defs map[Kind]*Definition
}

func NewRegistry() *Registry {
	return &Registry{defs: map[Kind]*Definition{}}
}

// Register adds or replaces a kind's definition. Replacing on a second
// Register is not an error — tests sometimes rebuild the registry with
// mocks.
func (r *Registry) Register(def *Definition) {
	if def == nil || def.Kind == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defs[def.Kind] = def
}

// Get returns the definition for a kind, or nil if not registered.
func (r *Registry) Get(k Kind) *Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defs[k]
}

// All returns every registered definition in a stable order (by Kind).
// Used by the admin Jobs page to enumerate what's available.
func (r *Registry) All() []*Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Definition, 0, len(r.defs))
	for _, d := range r.defs {
		out = append(out, d)
	}
	// Stable order so the UI doesn't shuffle on every page load.
	// Simple bubble — we have a handful of kinds.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Kind < out[i].Kind {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// MustGet is a convenience for call sites that are certain the kind is
// registered (e.g. a scheduler tick reading a kind out of
// job_schedules that was written by the same process).
func (r *Registry) MustGet(k Kind) *Definition {
	d := r.Get(k)
	if d == nil {
		panic(fmt.Sprintf("jobs: kind %q not registered", k))
	}
	return d
}
