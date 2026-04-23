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

type EnrichmentBatchRepo struct {
	db *pgxpool.Pool
}

func NewEnrichmentBatchRepo(db *pgxpool.Pool) *EnrichmentBatchRepo {
	return &EnrichmentBatchRepo{db: db}
}

// Create inserts a new enrichment batch and returns its ID.
func (r *EnrichmentBatchRepo) Create(ctx context.Context, batch *models.EnrichmentBatch) error {
	bookIDsJSON, err := json.Marshal(batch.BookIDs)
	if err != nil {
		return fmt.Errorf("marshaling book_ids: %w", err)
	}
	const q = `
		INSERT INTO enrichment_batches
		            (id, library_id, created_by, type, force, status, book_ids, total_books)
		VALUES      ($1, $2, $3, $4, $5, $6, $7, $8)`
	if _, err := r.db.Exec(ctx, q,
		batch.ID, batch.LibraryID, batch.CreatedBy,
		string(batch.Type), batch.Force, string(batch.Status),
		bookIDsJSON, batch.TotalBooks,
	); err != nil {
		return fmt.Errorf("inserting enrichment batch: %w", err)
	}
	return nil
}

// Get returns a single enrichment batch by ID (no items).
func (r *EnrichmentBatchRepo) Get(ctx context.Context, id uuid.UUID) (*models.EnrichmentBatch, error) {
	const q = `
		SELECT id, library_id, created_by, type, force, status, book_ids,
		       total_books, processed_books, failed_books, skipped_books, created_at, updated_at
		FROM   enrichment_batches
		WHERE  id = $1`
	row := r.db.QueryRow(ctx, q, id)
	batch, err := scanBatch(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return batch, err
}

// GetWithItems returns a batch including its per-book items.
func (r *EnrichmentBatchRepo) GetWithItems(ctx context.Context, id uuid.UUID) (*models.EnrichmentBatch, error) {
	batch, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	items, err := r.ListItems(ctx, id)
	if err != nil {
		return nil, err
	}
	batch.Items = items
	return batch, nil
}

// CreateItems inserts per-book item records for a batch.
func (r *EnrichmentBatchRepo) CreateItems(ctx context.Context, items []models.EnrichmentBatchItem) error {
	for _, item := range items {
		const q = `
			INSERT INTO enrichment_batch_items (id, batch_id, book_id, book_title, status)
			VALUES ($1, $2, $3, $4, $5)`
		if _, err := r.db.Exec(ctx, q, item.ID, item.BatchID, item.BookID, item.BookTitle, string(item.Status)); err != nil {
			return fmt.Errorf("inserting enrichment batch item: %w", err)
		}
	}
	return nil
}

// UpdateItemStatus updates a single item's status and message.
func (r *EnrichmentBatchRepo) UpdateItemStatus(ctx context.Context, id uuid.UUID, status models.EnrichmentBatchItemStatus, message string) error {
	const q = `
		UPDATE enrichment_batch_items
		SET    status = $2, message = $3, updated_at = now()
		WHERE  id = $1`
	if _, err := r.db.Exec(ctx, q, id, string(status), message); err != nil {
		return fmt.Errorf("updating enrichment batch item: %w", err)
	}
	return nil
}

// ListItems returns all items for a batch ordered by creation time.
func (r *EnrichmentBatchRepo) ListItems(ctx context.Context, batchID uuid.UUID) ([]models.EnrichmentBatchItem, error) {
	const q = `
		SELECT id, batch_id, book_id, book_title, status, message, created_at, updated_at
		FROM   enrichment_batch_items
		WHERE  batch_id = $1
		ORDER  BY created_at`
	rows, err := r.db.Query(ctx, q, batchID)
	if err != nil {
		return nil, fmt.Errorf("listing enrichment batch items: %w", err)
	}
	defer rows.Close()

	var out []models.EnrichmentBatchItem
	for rows.Next() {
		var (
			pgID      pgtype.UUID
			pgBatchID pgtype.UUID
			pgBookID  pgtype.UUID
			status    string
			item      models.EnrichmentBatchItem
		)
		if err := rows.Scan(&pgID, &pgBatchID, &pgBookID, &item.BookTitle, &status, &item.Message, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning enrichment batch item: %w", err)
		}
		item.ID = uuid.UUID(pgID.Bytes)
		item.BatchID = uuid.UUID(pgBatchID.Bytes)
		item.Status = models.EnrichmentBatchItemStatus(status)
		if pgBookID.Valid {
			id := uuid.UUID(pgBookID.Bytes)
			item.BookID = &id
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// FindItemByBookID returns the item for a specific book within a batch.
func (r *EnrichmentBatchRepo) FindItemByBookID(ctx context.Context, batchID, bookID uuid.UUID) (*models.EnrichmentBatchItem, error) {
	const q = `
		SELECT id, batch_id, book_id, book_title, status, message, created_at, updated_at
		FROM   enrichment_batch_items
		WHERE  batch_id = $1 AND book_id = $2`
	var (
		pgID      pgtype.UUID
		pgBatchID pgtype.UUID
		pgBookID  pgtype.UUID
		status    string
		item      models.EnrichmentBatchItem
	)
	err := r.db.QueryRow(ctx, q, batchID, bookID).Scan(
		&pgID, &pgBatchID, &pgBookID, &item.BookTitle, &status, &item.Message, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding enrichment batch item: %w", err)
	}
	item.ID = uuid.UUID(pgID.Bytes)
	item.BatchID = uuid.UUID(pgBatchID.Bytes)
	item.Status = models.EnrichmentBatchItemStatus(status)
	if pgBookID.Valid {
		id := uuid.UUID(pgBookID.Bytes)
		item.BookID = &id
	}
	return &item, nil
}

// ResyncCounters recalculates processed/failed/skipped counters from the items
// table so a resumed/retried batch has accurate progress rather than the stale
// values left by a previously crashed attempt.
func (r *EnrichmentBatchRepo) ResyncCounters(ctx context.Context, batchID uuid.UUID) error {
	const q = `
		UPDATE enrichment_batches b
		SET    processed_books = c.processed,
		       failed_books    = c.failed,
		       skipped_books   = c.skipped,
		       updated_at      = now()
		FROM (
			SELECT
				COUNT(*) FILTER (WHERE status IN ('done','failed','skipped')) AS processed,
				COUNT(*) FILTER (WHERE status = 'failed')                     AS failed,
				COUNT(*) FILTER (WHERE status = 'skipped')                    AS skipped
			FROM enrichment_batch_items
			WHERE batch_id = $1
		) c
		WHERE b.id = $1 AND b.status != 'cancelled'`
	if _, err := r.db.Exec(ctx, q, batchID); err != nil {
		return fmt.Errorf("resyncing batch counters: %w", err)
	}
	return nil
}

// UpdateStatus updates the status of a batch. Never overwrites a 'cancelled' status.
func (r *EnrichmentBatchRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status models.EnrichmentBatchStatus) error {
	const q = `
		UPDATE enrichment_batches
		SET    status = $2, updated_at = now()
		WHERE  id = $1 AND status != 'cancelled'`
	if _, err := r.db.Exec(ctx, q, id, string(status)); err != nil {
		return fmt.Errorf("updating enrichment batch status: %w", err)
	}
	return nil
}

// IncrementProcessed atomically increments the processed/failed/skipped counters.
// Never overwrites a 'cancelled' status. Returns the updated processed, failed, and
// total counts so the caller can determine if the batch is complete.
func (r *EnrichmentBatchRepo) IncrementProcessed(ctx context.Context, id uuid.UUID, failed, skipped bool) (processed, failedCount, total int, err error) {
	var q string
	switch {
	case failed:
		q = `
			UPDATE enrichment_batches
			SET    failed_books    = failed_books + 1,
			       processed_books = processed_books + 1,
			       updated_at      = now()
			WHERE  id = $1 AND status != 'cancelled'
			RETURNING processed_books, failed_books, total_books`
	case skipped:
		q = `
			UPDATE enrichment_batches
			SET    skipped_books   = skipped_books + 1,
			       processed_books = processed_books + 1,
			       updated_at      = now()
			WHERE  id = $1 AND status != 'cancelled'
			RETURNING processed_books, failed_books, total_books`
	default:
		q = `
			UPDATE enrichment_batches
			SET    processed_books = processed_books + 1,
			       updated_at      = now()
			WHERE  id = $1 AND status != 'cancelled'
			RETURNING processed_books, failed_books, total_books`
	}
	err = r.db.QueryRow(ctx, q, id).Scan(&processed, &failedCount, &total)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, 0, ErrNotFound
	}
	if err != nil {
		return 0, 0, 0, fmt.Errorf("incrementing processed count: %w", err)
	}
	return processed, failedCount, total, nil
}

// ListByUser returns all enrichment batches created by a user, newest first.
func (r *EnrichmentBatchRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]models.EnrichmentBatch, error) {
	const q = `
		SELECT eb.id, eb.library_id, eb.created_by, eb.type, eb.force, eb.status,
		       eb.book_ids, eb.total_books, eb.processed_books, eb.failed_books, eb.skipped_books,
		       eb.created_at, eb.updated_at, l.name
		FROM   enrichment_batches eb
		JOIN   libraries l ON l.id = eb.library_id
		WHERE  eb.created_by = $1
		ORDER  BY eb.created_at DESC`
	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("listing enrichment batches: %w", err)
	}
	defer rows.Close()

	var out []models.EnrichmentBatch
	for rows.Next() {
		batch, err := scanBatchWithLibraryName(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *batch)
	}
	return out, rows.Err()
}

// Cancel marks a pending or processing batch as cancelled.
func (r *EnrichmentBatchRepo) Cancel(ctx context.Context, batchID, userID uuid.UUID) error {
	const q = `
		UPDATE enrichment_batches
		SET    status = 'cancelled', updated_at = now()
		WHERE  id = $1 AND created_by = $2 AND status IN ('pending', 'processing')`
	tag, err := r.db.Exec(ctx, q, batchID, userID)
	if err != nil {
		return fmt.Errorf("cancelling enrichment batch: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a finished (done/failed/cancelled) batch owned by the user.
func (r *EnrichmentBatchRepo) Delete(ctx context.Context, batchID, userID uuid.UUID) error {
	const q = `
		DELETE FROM enrichment_batches
		WHERE  id = $1 AND created_by = $2 AND status IN ('done', 'failed', 'cancelled')`
	tag, err := r.db.Exec(ctx, q, batchID, userID)
	if err != nil {
		return fmt.Errorf("deleting enrichment batch: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteFinished removes all done/failed/cancelled batches created by the user.
func (r *EnrichmentBatchRepo) DeleteFinished(ctx context.Context, userID uuid.UUID) error {
	const q = `
		DELETE FROM enrichment_batches
		WHERE  created_by = $1 AND status IN ('done', 'failed', 'cancelled')`
	if _, err := r.db.Exec(ctx, q, userID); err != nil {
		return fmt.Errorf("deleting finished enrichment batches: %w", err)
	}
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

type batchScanner interface {
	Scan(dest ...any) error
}

func scanBatch(s batchScanner) (*models.EnrichmentBatch, error) {
	var (
		pgID        pgtype.UUID
		pgLibraryID pgtype.UUID
		pgCreatedBy pgtype.UUID
		batchType   string
		status      string
		bookIDsJSON []byte
		b           models.EnrichmentBatch
	)
	if err := s.Scan(
		&pgID, &pgLibraryID, &pgCreatedBy,
		&batchType, &b.Force, &status, &bookIDsJSON,
		&b.TotalBooks, &b.ProcessedBooks, &b.FailedBooks, &b.SkippedBooks,
		&b.CreatedAt, &b.UpdatedAt,
	); err != nil {
		return nil, err
	}
	b.ID = uuid.UUID(pgID.Bytes)
	if pgLibraryID.Valid {
		lid := uuid.UUID(pgLibraryID.Bytes)
		b.LibraryID = &lid
	}
	b.CreatedBy = uuid.UUID(pgCreatedBy.Bytes)
	b.Type = models.EnrichmentBatchType(batchType)
	b.Status = models.EnrichmentBatchStatus(status)
	if len(bookIDsJSON) > 0 {
		if err := json.Unmarshal(bookIDsJSON, &b.BookIDs); err != nil {
			return nil, fmt.Errorf("unmarshaling book_ids: %w", err)
		}
	}
	return &b, nil
}

func scanBatchWithLibraryName(s batchScanner) (*models.EnrichmentBatch, error) {
	var (
		pgID        pgtype.UUID
		pgLibraryID pgtype.UUID
		pgCreatedBy pgtype.UUID
		batchType   string
		status      string
		bookIDsJSON []byte
		b           models.EnrichmentBatch
	)
	if err := s.Scan(
		&pgID, &pgLibraryID, &pgCreatedBy,
		&batchType, &b.Force, &status, &bookIDsJSON,
		&b.TotalBooks, &b.ProcessedBooks, &b.FailedBooks, &b.SkippedBooks,
		&b.CreatedAt, &b.UpdatedAt, &b.LibraryName,
	); err != nil {
		return nil, fmt.Errorf("scanning enrichment batch: %w", err)
	}
	b.ID = uuid.UUID(pgID.Bytes)
	if pgLibraryID.Valid {
		lid := uuid.UUID(pgLibraryID.Bytes)
		b.LibraryID = &lid
	}
	b.CreatedBy = uuid.UUID(pgCreatedBy.Bytes)
	b.Type = models.EnrichmentBatchType(batchType)
	b.Status = models.EnrichmentBatchStatus(status)
	if len(bookIDsJSON) > 0 {
		if err := json.Unmarshal(bookIDsJSON, &b.BookIDs); err != nil {
			return nil, fmt.Errorf("unmarshaling book_ids: %w", err)
		}
	}
	return &b, nil
}
