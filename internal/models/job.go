// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// JobStatus is the canonical status for a row in the unified jobs table.
// Every kind uses this same set of values; kind-specific counters
// (rows/books/tokens) live on the kind's own table.
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCancelled JobStatus = "cancelled"
)

// JobTriggeredBy records who (or what) asked a job to run. Used for audit
// trails and to distinguish cost attribution (scheduler runs are free
// attention; user runs count against rate limits; admin runs bypass both).
type JobTriggeredBy string

const (
	JobTriggeredByUser      JobTriggeredBy = "user"
	JobTriggeredByAdmin     JobTriggeredBy = "admin"
	JobTriggeredByScheduler JobTriggeredBy = "scheduler"
	JobTriggeredByAPI       JobTriggeredBy = "api"
)

// Job is one execution of some kind of work — import, enrichment, AI
// suggestions, cover backfill, etc. Each kind may attach extra rows to
// its own table (see import_jobs, enrichment_batches,
// ai_suggestion_runs); the umbrella row is the source of truth for
// status + timestamps + event log.
type Job struct {
	ID          uuid.UUID
	Kind        string
	Status      JobStatus
	TriggeredBy JobTriggeredBy
	CreatedBy   *uuid.UUID // null when system-triggered
	ScheduleID  *uuid.UUID // set when this run was fired by a job_schedules row
	Error       string
	// Progress is a kind-specific JSON blob for summary UI. Import uses
	// {processed,failed,skipped,total}; enrichment similar; AI suggestions
	// uses {tokens_in,tokens_out,cost_usd}. Kept opaque at this layer.
	Progress    json.RawMessage
	StartedAt   *time.Time
	FinishedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// JobEvent is one entry in a job's progress log. Type is free-form per
// kind (e.g. "prompt" and "ai_response" for AI suggestions; "row_done"
// for imports). Clients render a generic timeline and let per-kind UI
// opt in to richer rendering.
type JobEvent struct {
	ID        uuid.UUID
	JobID     uuid.UUID
	Seq       int
	Type      string
	Content   json.RawMessage
	CreatedAt time.Time
}

// JobSchedule is a cron-driven recurring job. One row per Kind today;
// scope keys can be added later if we need per-user or per-library
// schedules.
type JobSchedule struct {
	ID          uuid.UUID
	Kind        string
	Cron        string // standard 5-field cron expression
	Enabled     bool
	Config      json.RawMessage // kind-specific tunables
	LastFiredAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
