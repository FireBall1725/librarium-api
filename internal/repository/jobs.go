// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JobRepo manages the umbrella jobs/job_events/job_schedules tables.
type JobRepo struct {
	db *pgxpool.Pool
}

func NewJobRepo(db *pgxpool.Pool) *JobRepo {
	return &JobRepo{db: db}
}

// ─── jobs ─────────────────────────────────────────────────────────────────────

// CreateJob inserts an umbrella row for a new run and returns the filled
// model (id, timestamps populated from the DB). Called from workers /
// services at the start of a run, before River is told to pick it up.
func (r *JobRepo) CreateJob(ctx context.Context, j *models.Job) error {
	const q = `
		INSERT INTO jobs (kind, status, triggered_by, created_by, schedule_id,
		                  error, progress, started_at, finished_at)
		VALUES           ($1, $2, $3, $4, $5, $6, COALESCE($7, '{}'::jsonb), $8, $9)
		RETURNING id, created_at, updated_at`
	progress := j.Progress
	if len(progress) == 0 {
		progress = json.RawMessage("{}")
	}
	status := j.Status
	if status == "" {
		status = models.JobStatusPending
	}
	trig := j.TriggeredBy
	if trig == "" {
		trig = models.JobTriggeredByUser
	}
	var pgID pgtype.UUID
	if err := r.db.QueryRow(ctx, q,
		j.Kind, string(status), string(trig),
		nullableUUID(j.CreatedBy), nullableUUID(j.ScheduleID),
		j.Error, progress, j.StartedAt, j.FinishedAt,
	).Scan(&pgID, &j.CreatedAt, &j.UpdatedAt); err != nil {
		return fmt.Errorf("inserting job: %w", err)
	}
	j.ID = uuid.UUID(pgID.Bytes)
	j.Status = status
	j.TriggeredBy = trig
	if len(j.Progress) == 0 {
		j.Progress = progress
	}
	return nil
}

// MarkRunning flips a job to running and stamps started_at if still null.
func (r *JobRepo) MarkRunning(ctx context.Context, jobID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		UPDATE jobs
		   SET status     = 'running',
		       started_at = COALESCE(started_at, NOW())
		 WHERE id = $1`, jobID)
	if err != nil {
		return fmt.Errorf("marking job running: %w", err)
	}
	return nil
}

// MarkFinished flips a job to completed or failed (based on err) and
// stamps finished_at. Errors are recorded on the row for the UI to
// surface. Idempotent — callers that might fire it more than once (e.g.
// panic handlers layered on a normal return) see the same final state.
func (r *JobRepo) MarkFinished(ctx context.Context, jobID uuid.UUID, status models.JobStatus, errMsg string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE jobs
		   SET status      = $2,
		       error       = $3,
		       finished_at = COALESCE(finished_at, NOW())
		 WHERE id = $1`, jobID, string(status), errMsg)
	if err != nil {
		return fmt.Errorf("marking job finished: %w", err)
	}
	return nil
}

// UpdateProgress replaces the progress JSON blob. Callers typically
// read-modify-write but we don't enforce atomicity here — kind-specific
// counter tables (import_job_items etc.) are authoritative; this is a
// denormalised summary the UI renders without joins.
func (r *JobRepo) UpdateProgress(ctx context.Context, jobID uuid.UUID, progress json.RawMessage) error {
	if len(progress) == 0 {
		progress = json.RawMessage("{}")
	}
	_, err := r.db.Exec(ctx, `UPDATE jobs SET progress = $2 WHERE id = $1`, jobID, progress)
	if err != nil {
		return fmt.Errorf("updating job progress: %w", err)
	}
	return nil
}

// GetJob fetches a single job by id. Returns ErrNotFound when no row.
func (r *JobRepo) GetJob(ctx context.Context, id uuid.UUID) (*models.Job, error) {
	const q = `
		SELECT id, kind, status, triggered_by, created_by, schedule_id,
		       error, progress, started_at, finished_at, created_at, updated_at
		  FROM jobs
		 WHERE id = $1`
	j, err := scanJob(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("fetching job: %w", err)
	}
	return j, nil
}

// ListJobsOpts filters ListJobs. Zero-value = no filter for that field.
type ListJobsOpts struct {
	Kind      string      // empty = any kind
	// Subtype only applies when Kind=='enrichment' — filters
	// enrichment_batches.type so the UI can split Metadata vs Covers without
	// exposing kind=enrichment&subtype=foo as a polluted filter combo.
	Subtype   string
	Status    string      // empty = any status
	CreatedBy *uuid.UUID  // nil = any caller
	Since     *time.Time  // nil = no time floor
	Limit     int         // 0 = default 50
	Offset    int
}

// ListJobs returns a paginated slice of jobs ordered newest first.
func (r *JobRepo) ListJobs(ctx context.Context, opts ListJobsOpts) ([]*models.Job, int, error) {
	where := []string{"1=1"}
	args := []any{}
	argIdx := 1

	if opts.Kind != "" {
		where = append(where, fmt.Sprintf("kind = $%d", argIdx))
		args = append(args, opts.Kind)
		argIdx++
	}
	// Subtype filter is enrichment-only; expressed as an EXISTS join so we
	// don't accidentally drop rows for other kinds when the option is set.
	if opts.Subtype != "" {
		where = append(where, fmt.Sprintf(`EXISTS (
			SELECT 1 FROM enrichment_batches eb
			WHERE eb.job_id = jobs.id AND eb.type = $%d
		)`, argIdx))
		args = append(args, opts.Subtype)
		argIdx++
	}
	if opts.Status != "" {
		where = append(where, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, opts.Status)
		argIdx++
	}
	if opts.CreatedBy != nil {
		where = append(where, fmt.Sprintf("created_by = $%d", argIdx))
		args = append(args, *opts.CreatedBy)
		argIdx++
	}
	if opts.Since != nil {
		where = append(where, fmt.Sprintf("created_at >= $%d", argIdx))
		args = append(args, *opts.Since)
		argIdx++
	}
	whereSQL := ""
	for i, w := range where {
		if i == 0 {
			whereSQL = "WHERE " + w
		} else {
			whereSQL += " AND " + w
		}
	}

	limit := opts.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	var total int
	if err := r.db.QueryRow(ctx,
		"SELECT COUNT(*) FROM jobs "+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting jobs: %w", err)
	}

	args = append(args, limit, opts.Offset)
	q := `
		SELECT id, kind, status, triggered_by, created_by, schedule_id,
		       error, progress, started_at, finished_at, created_at, updated_at
		  FROM jobs ` + whereSQL + fmt.Sprintf(`
		 ORDER BY created_at DESC
		 LIMIT $%d OFFSET $%d`, argIdx, argIdx+1)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing jobs: %w", err)
	}
	defer rows.Close()

	var out []*models.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, j)
	}
	return out, total, rows.Err()
}

// DeleteJob removes a job and cascades through kind-specific tables +
// job_events via ON DELETE CASCADE.
func (r *JobRepo) DeleteJob(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM jobs WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteFinished hard-deletes every job row in a terminal state
// (completed/failed/cancelled). Used by the "Clear history" admin action.
// Includes legacy 'done' for rows written before the status normalizer.
// Returns the deleted count.
func (r *JobRepo) DeleteFinished(ctx context.Context, kind string) (int64, error) {
	where := "status IN ('completed','done','failed','cancelled')"
	args := []any{}
	if kind != "" {
		where += " AND kind = $1"
		args = append(args, kind)
	}
	tag, err := r.db.Exec(ctx, "DELETE FROM jobs WHERE "+where, args...)
	if err != nil {
		return 0, fmt.Errorf("deleting finished jobs: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ─── job_events ───────────────────────────────────────────────────────────────

// AppendEvent writes one pipeline event tied to a job. Sequence is
// computed server-side via COALESCE(MAX(seq)+1, 0) so concurrent callers
// don't race each other. Content is JSON-encoded server-side when passed
// as any; pre-marshaled bytes are used as-is.
func (r *JobRepo) AppendEvent(ctx context.Context, jobID uuid.UUID, eventType string, content any) error {
	var payload []byte
	switch v := content.(type) {
	case nil:
		payload = []byte("{}")
	case []byte:
		payload = v
	case json.RawMessage:
		payload = v
	default:
		b, err := json.Marshal(content)
		if err != nil {
			return fmt.Errorf("marshal event content: %w", err)
		}
		payload = b
	}
	const q = `
		INSERT INTO job_events (job_id, seq, type, content)
		VALUES ($1, COALESCE((SELECT MAX(seq)+1 FROM job_events WHERE job_id = $1), 0), $2, $3)`
	if _, err := r.db.Exec(ctx, q, jobID, eventType, payload); err != nil {
		return fmt.Errorf("inserting job event: %w", err)
	}
	return nil
}

// ListEvents returns every event for a job in sequence order.
func (r *JobRepo) ListEvents(ctx context.Context, jobID uuid.UUID) ([]*models.JobEvent, error) {
	const q = `
		SELECT id, job_id, seq, type, content, created_at
		  FROM job_events
		 WHERE job_id = $1
		 ORDER BY seq ASC`
	rows, err := r.db.Query(ctx, q, jobID)
	if err != nil {
		return nil, fmt.Errorf("listing job events: %w", err)
	}
	defer rows.Close()

	var out []*models.JobEvent
	for rows.Next() {
		var (
			pgID    pgtype.UUID
			pgJobID pgtype.UUID
			e       models.JobEvent
		)
		if err := rows.Scan(&pgID, &pgJobID, &e.Seq, &e.Type, &e.Content, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.ID = uuid.UUID(pgID.Bytes)
		e.JobID = uuid.UUID(pgJobID.Bytes)
		out = append(out, &e)
	}
	return out, rows.Err()
}

// ─── job_schedules ────────────────────────────────────────────────────────────

// GetSchedule returns the schedule row for a kind, or ErrNotFound.
func (r *JobRepo) GetSchedule(ctx context.Context, kind string) (*models.JobSchedule, error) {
	const q = `
		SELECT id, kind, cron, enabled, config, last_fired_at, created_at, updated_at
		  FROM job_schedules
		 WHERE kind = $1`
	s, err := scanSchedule(r.db.QueryRow(ctx, q, kind))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("fetching schedule: %w", err)
	}
	return s, nil
}

// ListSchedules returns every schedule row, ordered by kind.
func (r *JobRepo) ListSchedules(ctx context.Context) ([]*models.JobSchedule, error) {
	const q = `
		SELECT id, kind, cron, enabled, config, last_fired_at, created_at, updated_at
		  FROM job_schedules
		 ORDER BY kind`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("listing schedules: %w", err)
	}
	defer rows.Close()
	var out []*models.JobSchedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpsertSchedule creates or updates the schedule for a kind.
func (r *JobRepo) UpsertSchedule(ctx context.Context, s *models.JobSchedule) error {
	config := s.Config
	if len(config) == 0 {
		config = json.RawMessage("{}")
	}
	const q = `
		INSERT INTO job_schedules (kind, cron, enabled, config)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (kind) DO UPDATE
		   SET cron    = EXCLUDED.cron,
		       enabled = EXCLUDED.enabled,
		       config  = EXCLUDED.config,
		       updated_at = NOW()
		RETURNING id, created_at, updated_at`
	var pgID pgtype.UUID
	if err := r.db.QueryRow(ctx, q, s.Kind, s.Cron, s.Enabled, config).
		Scan(&pgID, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return fmt.Errorf("upserting schedule: %w", err)
	}
	s.ID = uuid.UUID(pgID.Bytes)
	return nil
}

// MarkScheduleFired stamps last_fired_at = NOW() on a schedule. Used by
// the scheduler loop after it enqueues a run so the next tick doesn't
// immediately re-fire.
func (r *JobRepo) MarkScheduleFired(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE job_schedules SET last_fired_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("marking schedule fired: %w", err)
	}
	return nil
}

// ─── scanners ─────────────────────────────────────────────────────────────────

func scanJob(s scanner) (*models.Job, error) {
	var (
		pgID         pgtype.UUID
		pgCreatedBy  pgtype.UUID
		pgScheduleID pgtype.UUID
		status       string
		triggeredBy  string
		progress     []byte
		j            models.Job
	)
	if err := s.Scan(
		&pgID, &j.Kind, &status, &triggeredBy, &pgCreatedBy, &pgScheduleID,
		&j.Error, &progress, &j.StartedAt, &j.FinishedAt, &j.CreatedAt, &j.UpdatedAt,
	); err != nil {
		return nil, err
	}
	j.ID = uuid.UUID(pgID.Bytes)
	j.Status = models.JobStatus(status)
	j.TriggeredBy = models.JobTriggeredBy(triggeredBy)
	if pgCreatedBy.Valid {
		id := uuid.UUID(pgCreatedBy.Bytes)
		j.CreatedBy = &id
	}
	if pgScheduleID.Valid {
		id := uuid.UUID(pgScheduleID.Bytes)
		j.ScheduleID = &id
	}
	j.Progress = progress
	return &j, nil
}

func scanSchedule(s scanner) (*models.JobSchedule, error) {
	var (
		pgID    pgtype.UUID
		config  []byte
		sched   models.JobSchedule
	)
	if err := s.Scan(
		&pgID, &sched.Kind, &sched.Cron, &sched.Enabled, &config,
		&sched.LastFiredAt, &sched.CreatedAt, &sched.UpdatedAt,
	); err != nil {
		return nil, err
	}
	sched.ID = uuid.UUID(pgID.Bytes)
	sched.Config = config
	return &sched, nil
}

// normalizeStatusForJobs translates the legacy status vocabulary
// (`processing`/`done`) used by import_jobs and enrichment_batches into
// the canonical values on the umbrella jobs table (`running`/`completed`).
// Other values (pending/failed/cancelled) pass through unchanged. Used by
// the mirroring paths on those two repos; AI suggestions already uses
// the canonical set directly.
func normalizeStatusForJobs(s string) string {
	switch s {
	case "processing":
		return "running"
	case "done":
		return "completed"
	default:
		return s
	}
}

// nullableUUID is a small helper — pgx accepts *uuid.UUID natively but
// a pgtype.UUID with Valid=false is clearer at the call site when the
// caller already has a *uuid.UUID in hand.
func nullableUUID(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return *id
}
