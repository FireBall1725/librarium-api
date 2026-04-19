// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ImportJobRepo struct {
	db *pgxpool.Pool
}

func NewImportJobRepo(db *pgxpool.Pool) *ImportJobRepo {
	return &ImportJobRepo{db: db}
}

// CreateJob inserts a new import job and its items in a single transaction.
func (r *ImportJobRepo) CreateJob(ctx context.Context, job *models.ImportJob, items []models.ImportJobItem) error {
	optionsJSON, err := json.Marshal(job.Options)
	if err != nil {
		return fmt.Errorf("marshaling options: %w", err)
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	const qJob = `
		INSERT INTO import_jobs (id, library_id, created_by, status, total_rows, options)
		VALUES ($1, $2, $3, $4, $5, $6)`
	if _, err := tx.Exec(ctx, qJob,
		job.ID, job.LibraryID, job.CreatedBy,
		string(job.Status), job.TotalRows, optionsJSON,
	); err != nil {
		return fmt.Errorf("inserting import job: %w", err)
	}

	for _, item := range items {
		rawJSON, err := json.Marshal(item.RawData)
		if err != nil {
			return fmt.Errorf("marshaling raw data: %w", err)
		}
		const qItem = `
			INSERT INTO import_job_items (id, import_job_id, row_number, raw_data, title, isbn)
			VALUES ($1, $2, $3, $4, $5, $6)`
		if _, err := tx.Exec(ctx, qItem,
			item.ID, item.ImportJobID, item.RowNumber, rawJSON, item.Title, item.ISBN,
		); err != nil {
			return fmt.Errorf("inserting import job item: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// GetJob returns an import job with all its items.
func (r *ImportJobRepo) GetJob(ctx context.Context, id uuid.UUID) (*models.ImportJob, error) {
	const qJob = `
		SELECT id, library_id, created_by, status, total_rows, processed_rows, failed_rows, skipped_rows, options, created_at, updated_at
		FROM import_jobs WHERE id = $1`

	var (
		pgID        pgtype.UUID
		pgLibraryID pgtype.UUID
		pgCreatedBy pgtype.UUID
		optJSON     []byte
		job         models.ImportJob
	)
	row := r.db.QueryRow(ctx, qJob, id)
	if err := row.Scan(
		&pgID, &pgLibraryID, &pgCreatedBy,
		&job.Status, &job.TotalRows, &job.ProcessedRows, &job.FailedRows, &job.SkippedRows,
		&optJSON, &job.CreatedAt, &job.UpdatedAt,
	); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("scanning import job: %w", err)
	}
	job.ID = uuid.UUID(pgID.Bytes)
	job.LibraryID = uuid.UUID(pgLibraryID.Bytes)
	job.CreatedBy = uuid.UUID(pgCreatedBy.Bytes)
	if err := json.Unmarshal(optJSON, &job.Options); err != nil {
		return nil, fmt.Errorf("unmarshaling options: %w", err)
	}

	items, err := r.listItems(ctx, id)
	if err != nil {
		return nil, err
	}
	job.Items = items
	return &job, nil
}

// GetJobByLibrary returns an import job only if it belongs to the given library.
func (r *ImportJobRepo) GetJobByLibrary(ctx context.Context, libraryID, jobID uuid.UUID) (*models.ImportJob, error) {
	job, err := r.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job.LibraryID != libraryID {
		return nil, ErrNotFound
	}
	return job, nil
}

func (r *ImportJobRepo) listItems(ctx context.Context, jobID uuid.UUID) ([]models.ImportJobItem, error) {
	const q = `
		SELECT id, import_job_id, row_number, raw_data, status, title, isbn, message, book_id, created_at, updated_at
		FROM import_job_items
		WHERE import_job_id = $1
		ORDER BY row_number`
	rows, err := r.db.Query(ctx, q, jobID)
	if err != nil {
		return nil, fmt.Errorf("listing import job items: %w", err)
	}
	defer rows.Close()

	var out []models.ImportJobItem
	for rows.Next() {
		item, err := scanImportItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	return out, rows.Err()
}

// UpdateJobStatus updates the status and counters of a job.
// It never overwrites a 'cancelled' status so a user cancel cannot be undone by the worker.
func (r *ImportJobRepo) UpdateJobStatus(ctx context.Context, id uuid.UUID, status models.ImportJobStatus, processed, failed, skipped int) error {
	const q = `
		UPDATE import_jobs
		SET status = $2, processed_rows = $3, failed_rows = $4, skipped_rows = $5, updated_at = now()
		WHERE id = $1 AND status != 'cancelled'`
	if _, err := r.db.Exec(ctx, q, id, string(status), processed, failed, skipped); err != nil {
		return fmt.Errorf("updating import job status: %w", err)
	}
	return nil
}

// UpdateItemStatus updates a single item's status and message.
func (r *ImportJobRepo) UpdateItemStatus(ctx context.Context, id uuid.UUID, status models.ImportItemStatus, message string, bookID *uuid.UUID) error {
	const q = `
		UPDATE import_job_items
		SET status = $2, message = $3, book_id = $4, updated_at = now()
		WHERE id = $1`
	if _, err := r.db.Exec(ctx, q, id, string(status), message, bookID); err != nil {
		return fmt.Errorf("updating import job item: %w", err)
	}
	return nil
}

// ListByUser returns all import jobs created by a user across all libraries, newest first.
// The library name is populated via a JOIN.
func (r *ImportJobRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]models.ImportJob, error) {
	const q = `
		SELECT ij.id, ij.library_id, ij.created_by, ij.status,
		       ij.total_rows, ij.processed_rows, ij.failed_rows, ij.skipped_rows,
		       ij.options, ij.created_at, ij.updated_at, l.name
		FROM import_jobs ij
		JOIN libraries l ON l.id = ij.library_id
		WHERE ij.created_by = $1
		ORDER BY ij.created_at DESC`
	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("listing import jobs by user: %w", err)
	}
	defer rows.Close()

	var out []models.ImportJob
	for rows.Next() {
		var (
			pgID        pgtype.UUID
			pgLibraryID pgtype.UUID
			pgCreatedBy pgtype.UUID
			optJSON     []byte
			job         models.ImportJob
		)
		if err := rows.Scan(
			&pgID, &pgLibraryID, &pgCreatedBy,
			&job.Status, &job.TotalRows, &job.ProcessedRows, &job.FailedRows, &job.SkippedRows,
			&optJSON, &job.CreatedAt, &job.UpdatedAt, &job.LibraryName,
		); err != nil {
			return nil, fmt.Errorf("scanning import job: %w", err)
		}
		job.ID = uuid.UUID(pgID.Bytes)
		job.LibraryID = uuid.UUID(pgLibraryID.Bytes)
		job.CreatedBy = uuid.UUID(pgCreatedBy.Bytes)
		if err := json.Unmarshal(optJSON, &job.Options); err != nil {
			return nil, fmt.Errorf("unmarshaling options: %w", err)
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

// CancelJob marks a pending or processing import job as cancelled.
// Only the job's creator can cancel it.
func (r *ImportJobRepo) CancelJob(ctx context.Context, jobID, userID uuid.UUID) error {
	const q = `
		UPDATE import_jobs
		SET status = 'cancelled', updated_at = now()
		WHERE id = $1 AND created_by = $2 AND status IN ('pending', 'processing')`
	tag, err := r.db.Exec(ctx, q, jobID, userID)
	if err != nil {
		return fmt.Errorf("cancelling import job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByLibrary returns all import jobs for a library, newest first, without items.
func (r *ImportJobRepo) ListByLibrary(ctx context.Context, libraryID uuid.UUID) ([]models.ImportJob, error) {
	const q = `
		SELECT id, library_id, created_by, status, total_rows, processed_rows, failed_rows, skipped_rows, options, created_at, updated_at
		FROM import_jobs
		WHERE library_id = $1
		ORDER BY created_at DESC`
	rows, err := r.db.Query(ctx, q, libraryID)
	if err != nil {
		return nil, fmt.Errorf("listing import jobs: %w", err)
	}
	defer rows.Close()

	var out []models.ImportJob
	for rows.Next() {
		var (
			pgID        pgtype.UUID
			pgLibraryID pgtype.UUID
			pgCreatedBy pgtype.UUID
			optJSON     []byte
			job         models.ImportJob
		)
		if err := rows.Scan(
			&pgID, &pgLibraryID, &pgCreatedBy,
			&job.Status, &job.TotalRows, &job.ProcessedRows, &job.FailedRows, &job.SkippedRows,
			&optJSON, &job.CreatedAt, &job.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning import job: %w", err)
		}
		job.ID = uuid.UUID(pgID.Bytes)
		job.LibraryID = uuid.UUID(pgLibraryID.Bytes)
		job.CreatedBy = uuid.UUID(pgCreatedBy.Bytes)
		if err := json.Unmarshal(optJSON, &job.Options); err != nil {
			return nil, fmt.Errorf("unmarshaling options: %w", err)
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

// DeleteJob removes a finished (done/failed/cancelled) import job owned by the user.
// Returns ErrNotFound if no matching row is affected.
func (r *ImportJobRepo) DeleteJob(ctx context.Context, jobID, userID uuid.UUID) error {
	const q = `
		DELETE FROM import_jobs
		WHERE id = $1 AND created_by = $2 AND status IN ('done', 'failed', 'cancelled')`
	tag, err := r.db.Exec(ctx, q, jobID, userID)
	if err != nil {
		return fmt.Errorf("deleting import job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteFinishedJobs removes all done/failed/cancelled jobs created by the user.
func (r *ImportJobRepo) DeleteFinishedJobs(ctx context.Context, userID uuid.UUID) error {
	const q = `
		DELETE FROM import_jobs
		WHERE created_by = $1 AND status IN ('done', 'failed', 'cancelled')`
	if _, err := r.db.Exec(ctx, q, userID); err != nil {
		return fmt.Errorf("deleting finished import jobs: %w", err)
	}
	return nil
}

// ListPendingItems returns all pending items for a job, ordered by row_number.
func (r *ImportJobRepo) ListPendingItems(ctx context.Context, jobID uuid.UUID) ([]models.ImportJobItem, error) {
	const q = `
		SELECT id, import_job_id, row_number, raw_data, status, title, isbn, message, book_id, created_at, updated_at
		FROM import_job_items
		WHERE import_job_id = $1 AND status = 'pending'
		ORDER BY row_number`
	rows, err := r.db.Query(ctx, q, jobID)
	if err != nil {
		return nil, fmt.Errorf("listing pending items: %w", err)
	}
	defer rows.Close()

	var out []models.ImportJobItem
	for rows.Next() {
		item, err := scanImportItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	return out, rows.Err()
}

func scanImportItem(s scanner) (*models.ImportJobItem, error) {
	var (
		pgID     pgtype.UUID
		pgJobID  pgtype.UUID
		pgBookID pgtype.UUID
		rawJSON  []byte
		item     models.ImportJobItem
	)

	if err := s.Scan(
		&pgID, &pgJobID, &item.RowNumber, &rawJSON,
		&item.Status, &item.Title, &item.ISBN, &item.Message,
		&pgBookID, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("scanning import item: %w", err)
	}
	item.ID = uuid.UUID(pgID.Bytes)
	item.ImportJobID = uuid.UUID(pgJobID.Bytes)
	if pgBookID.Valid {
		id := uuid.UUID(pgBookID.Bytes)
		item.BookID = &id
	}
	if len(rawJSON) > 0 {
		if err := json.Unmarshal(rawJSON, &item.RawData); err != nil {
			return nil, fmt.Errorf("unmarshaling raw data: %w", err)
		}
	}
	return &item, nil
}
