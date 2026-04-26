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

// AIMetadataRepo wraps both ai_metadata_runs and ai_metadata_proposals — they
// are tightly coupled (every proposal points at a run) and almost always
// queried together in the UI.
type AIMetadataRepo struct {
	db *pgxpool.Pool
}

func NewAIMetadataRepo(db *pgxpool.Pool) *AIMetadataRepo {
	return &AIMetadataRepo{db: db}
}

// ─── Runs ─────────────────────────────────────────────────────────────────────

// CreateRun inserts a new run row in status='running' and returns its ID. The
// service calls this before invoking the AI so partial failures (timeouts mid-
// call) still produce an audit row to investigate.
func (r *AIMetadataRepo) CreateRun(ctx context.Context, libraryID *uuid.UUID, jobID *uuid.UUID, triggeredBy *uuid.UUID, kind, targetType string, targetID uuid.UUID, providerType, modelID, prompt string) (uuid.UUID, error) {
	const q = `
		INSERT INTO ai_metadata_runs (
			library_id, job_id, triggered_by, kind, target_type, target_id,
			provider_type, model_id, status, prompt
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'running', $9)
		RETURNING id`
	var id uuid.UUID
	if err := r.db.QueryRow(ctx, q, libraryID, jobID, triggeredBy, kind, targetType, targetID, providerType, modelID, prompt).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("create ai_metadata_run: %w", err)
	}
	return id, nil
}

// CreateJob inserts an umbrella jobs row of kind='ai_metadata_proposal' and
// returns the new job_id. Used by the synchronous suggest-* endpoints so each
// AI call surfaces in the unified jobs history alongside imports / enrichment
// batches / etc.
func (r *AIMetadataRepo) CreateJob(ctx context.Context, createdBy uuid.UUID) (uuid.UUID, error) {
	const q = `
		INSERT INTO jobs (kind, status, triggered_by, created_by, started_at)
		VALUES ('ai_metadata_proposal', 'running', 'user', $1, NOW())
		RETURNING id`
	var id uuid.UUID
	if err := r.db.QueryRow(ctx, q, createdBy).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("create ai metadata job: %w", err)
	}
	return id, nil
}

// FinishRun records the response, usage, and final status. Callers pass
// status as one of AIMetaRunStatus*. When the run is linked to an umbrella
// jobs row (job_id non-null), this also mirrors the final status onto that
// row so the unified jobs history reflects the outcome.
func (r *AIMetadataRepo) FinishRun(ctx context.Context, runID uuid.UUID, status, errMsg, responseText string, tokensIn, tokensOut int, costUSD float64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	const q = `
		UPDATE ai_metadata_runs
		SET status = $2, error = NULLIF($3, ''), response_text = $4,
		    tokens_in = $5, tokens_out = $6, estimated_cost_usd = $7,
		    finished_at = $8
		WHERE id = $1
		RETURNING job_id`
	var pgJobID pgtype.UUID
	if err := tx.QueryRow(ctx, q, runID, status, errMsg, responseText, tokensIn, tokensOut, costUSD, time.Now()).Scan(&pgJobID); err != nil {
		return fmt.Errorf("finish ai_metadata_run: %w", err)
	}
	if pgJobID.Valid {
		// Map our status vocabulary onto the jobs umbrella's:
		//   ai_metadata_runs: running | completed | failed | skipped
		//   jobs:             pending | running | completed | failed | cancelled
		jobStatus := status
		if status == models.AIMetaRunStatusSkipped {
			jobStatus = "completed"
		}
		const updJob = `
			UPDATE jobs
			SET status = $2, error = $3,
			    progress = jsonb_build_object('tokens_in', $4::int, 'tokens_out', $5::int, 'cost_usd', $6::numeric),
			    finished_at = COALESCE(finished_at, NOW())
			WHERE id = $1`
		if _, err := tx.Exec(ctx, updJob, uuid.UUID(pgJobID.Bytes), jobStatus, errMsg, tokensIn, tokensOut, costUSD); err != nil {
			return fmt.Errorf("updating umbrella job: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// GetRun fetches a single run by id.
func (r *AIMetadataRepo) GetRun(ctx context.Context, id uuid.UUID) (*models.AIMetadataRun, error) {
	const q = `
		SELECT id, library_id, job_id, triggered_by, kind, target_type, target_id,
		       provider_type, model_id, status, COALESCE(error, ''),
		       tokens_in, tokens_out, estimated_cost_usd,
		       COALESCE(prompt, ''), COALESCE(response_text, ''),
		       started_at, finished_at
		FROM ai_metadata_runs WHERE id = $1`
	row := r.db.QueryRow(ctx, q, id)
	run, err := scanAIMetadataRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get ai_metadata_run: %w", err)
	}
	return run, nil
}

// ListRunsForJob returns every run associated with a given river job id, in
// chronological order. Used by the jobs run-detail panel to render expandable
// per-call sections inside one batch.
func (r *AIMetadataRepo) ListRunsForJob(ctx context.Context, jobID uuid.UUID) ([]*models.AIMetadataRun, error) {
	const q = `
		SELECT id, library_id, job_id, triggered_by, kind, target_type, target_id,
		       provider_type, model_id, status, COALESCE(error, ''),
		       tokens_in, tokens_out, estimated_cost_usd,
		       COALESCE(prompt, ''), COALESCE(response_text, ''),
		       started_at, finished_at
		FROM ai_metadata_runs
		WHERE job_id = $1
		ORDER BY started_at`
	rows, err := r.db.Query(ctx, q, jobID)
	if err != nil {
		return nil, fmt.Errorf("list runs for job: %w", err)
	}
	defer rows.Close()
	var out []*models.AIMetadataRun
	for rows.Next() {
		run, err := scanAIMetadataRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// ─── Proposals ────────────────────────────────────────────────────────────────

// CreateProposal inserts a pending proposal linked to a run.
func (r *AIMetadataRepo) CreateProposal(ctx context.Context, libraryID, targetID uuid.UUID, runID *uuid.UUID, targetType, kind string, payload json.RawMessage) (uuid.UUID, error) {
	const q = `
		INSERT INTO ai_metadata_proposals (library_id, run_id, target_type, target_id, kind, payload)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`
	var id uuid.UUID
	if err := r.db.QueryRow(ctx, q, libraryID, runID, targetType, targetID, kind, payload).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("create proposal: %w", err)
	}
	return id, nil
}

// ListProposals returns proposals for a target, optionally filtered by status
// (empty string = no filter).
func (r *AIMetadataRepo) ListProposals(ctx context.Context, targetType string, targetID uuid.UUID, statusFilter string) ([]*models.AIMetadataProposal, error) {
	args := []any{targetType, targetID}
	where := `WHERE target_type = $1 AND target_id = $2`
	if statusFilter != "" {
		args = append(args, statusFilter)
		where += fmt.Sprintf(` AND status = $%d`, len(args))
	}
	q := `
		SELECT id, library_id, run_id, target_type, target_id, kind, payload,
		       status, created_at, applied_at, applied_by
		FROM ai_metadata_proposals
		` + where + `
		ORDER BY created_at DESC`
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list proposals: %w", err)
	}
	defer rows.Close()
	var out []*models.AIMetadataProposal
	for rows.Next() {
		p, err := scanAIMetadataProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProposal fetches a single proposal by id.
func (r *AIMetadataRepo) GetProposal(ctx context.Context, id uuid.UUID) (*models.AIMetadataProposal, error) {
	const q = `
		SELECT id, library_id, run_id, target_type, target_id, kind, payload,
		       status, created_at, applied_at, applied_by
		FROM ai_metadata_proposals WHERE id = $1`
	row := r.db.QueryRow(ctx, q, id)
	p, err := scanAIMetadataProposal(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get proposal: %w", err)
	}
	return p, nil
}

// MarkProposalApplied transitions a proposal to accepted (or partially_accepted)
// and records who applied it. The actual writes to the target series happen in
// the service layer.
func (r *AIMetadataRepo) MarkProposalApplied(ctx context.Context, id, appliedBy uuid.UUID, fullAccept bool) error {
	status := models.AIMetaProposalStatusAccepted
	if !fullAccept {
		status = models.AIMetaProposalStatusPartiallyAccepted
	}
	const q = `
		UPDATE ai_metadata_proposals
		SET status = $2, applied_at = NOW(), applied_by = $3
		WHERE id = $1`
	if _, err := r.db.Exec(ctx, q, id, status, appliedBy); err != nil {
		return fmt.Errorf("mark proposal applied: %w", err)
	}
	return nil
}

// MarkProposalRejected transitions a proposal to rejected.
func (r *AIMetadataRepo) MarkProposalRejected(ctx context.Context, id uuid.UUID) error {
	const q = `
		UPDATE ai_metadata_proposals
		SET status = $2, applied_at = NOW()
		WHERE id = $1`
	if _, err := r.db.Exec(ctx, q, id, models.AIMetaProposalStatusRejected); err != nil {
		return fmt.Errorf("mark proposal rejected: %w", err)
	}
	return nil
}

// ─── Scanners ─────────────────────────────────────────────────────────────────

func scanAIMetadataRun(s scanner) (*models.AIMetadataRun, error) {
	var (
		pgID          pgtype.UUID
		pgLibrary     pgtype.UUID
		pgJob         pgtype.UUID
		pgTriggeredBy pgtype.UUID
		pgTargetID    pgtype.UUID
		pgFinishedAt  pgtype.Timestamptz
		run           models.AIMetadataRun
	)
	err := s.Scan(
		&pgID, &pgLibrary, &pgJob, &pgTriggeredBy, &run.Kind, &run.TargetType, &pgTargetID,
		&run.ProviderType, &run.ModelID, &run.Status, &run.Error,
		&run.TokensIn, &run.TokensOut, &run.EstimatedCostUSD,
		&run.Prompt, &run.ResponseText,
		&run.StartedAt, &pgFinishedAt,
	)
	if err != nil {
		return nil, err
	}
	run.ID = uuid.UUID(pgID.Bytes)
	run.TargetID = uuid.UUID(pgTargetID.Bytes)
	if pgLibrary.Valid {
		id := uuid.UUID(pgLibrary.Bytes)
		run.LibraryID = &id
	}
	if pgJob.Valid {
		id := uuid.UUID(pgJob.Bytes)
		run.JobID = &id
	}
	if pgTriggeredBy.Valid {
		id := uuid.UUID(pgTriggeredBy.Bytes)
		run.TriggeredBy = &id
	}
	if pgFinishedAt.Valid {
		t := pgFinishedAt.Time
		run.FinishedAt = &t
	}
	return &run, nil
}

func scanAIMetadataProposal(s scanner) (*models.AIMetadataProposal, error) {
	var (
		pgID         pgtype.UUID
		pgLibrary    pgtype.UUID
		pgRunID      pgtype.UUID
		pgTargetID   pgtype.UUID
		pgAppliedAt  pgtype.Timestamptz
		pgAppliedBy  pgtype.UUID
		payloadBytes []byte
		p            models.AIMetadataProposal
	)
	err := s.Scan(
		&pgID, &pgLibrary, &pgRunID, &p.TargetType, &pgTargetID, &p.Kind, &payloadBytes,
		&p.Status, &p.CreatedAt, &pgAppliedAt, &pgAppliedBy,
	)
	if err != nil {
		return nil, err
	}
	p.ID = uuid.UUID(pgID.Bytes)
	p.LibraryID = uuid.UUID(pgLibrary.Bytes)
	p.TargetID = uuid.UUID(pgTargetID.Bytes)
	p.Payload = payloadBytes
	if pgRunID.Valid {
		id := uuid.UUID(pgRunID.Bytes)
		p.RunID = &id
	}
	if pgAppliedAt.Valid {
		t := pgAppliedAt.Time
		p.AppliedAt = &t
	}
	if pgAppliedBy.Valid {
		id := uuid.UUID(pgAppliedBy.Bytes)
		p.AppliedBy = &id
	}
	return &p, nil
}
