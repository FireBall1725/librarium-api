// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/responses"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Apply status values returned by ApplyUserBookInteractionOp.
const (
	SyncApplyStatusApplied        = "applied"
	SyncApplyStatusDiscardedStale = "discarded_stale"
	SyncApplyStatusNotFound       = "not_found"
	SyncApplyStatusInvalid        = "invalid"
)

type SyncRepo struct {
	db *pgxpool.Pool
}

func NewSyncRepo(db *pgxpool.Pool) *SyncRepo {
	return &SyncRepo{db: db}
}

// UserBookInteractionChanges returns up to `limit` ops for the given
// user's interactions that have a per-field updated_at, row updated_at,
// or deleted_at strictly greater than `since`. Ordered by row updated_at
// ascending so the client can advance its sync cursor by taking the max
// updated_at of the returned ops.
//
// Per-field LWW emits one op per changed field. A tombstone (deleted_at)
// emits a single Deleted=true op and suppresses the per-field ops for
// that row (the row is gone; per-field state is moot).
func (r *SyncRepo) UserBookInteractionChanges(ctx context.Context, userID uuid.UUID, since time.Time, limit int) ([]responses.SyncOp, error) {
	const q = `
		SELECT id,
		       rating, rating_updated_at,
		       read_status, read_status_updated_at,
		       progress, progress_updated_at,
		       is_favorite, is_favorite_updated_at,
		       updated_at, deleted_at
		  FROM user_book_interactions
		 WHERE user_id = $1
		   AND (
		           updated_at             > $2
		        OR rating_updated_at      > $2
		        OR read_status_updated_at > $2
		        OR progress_updated_at    > $2
		        OR is_favorite_updated_at > $2
		        OR deleted_at             > $2
		       )
		 ORDER BY updated_at ASC
		 LIMIT $3`
	rows, err := r.db.Query(ctx, q, userID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("sync ubi changes: %w", err)
	}
	defer rows.Close()

	var ops []responses.SyncOp
	for rows.Next() {
		var (
			id           uuid.UUID
			rating       *int16
			ratingTS     *time.Time
			readStatus   string
			readStatusTS *time.Time
			progressRaw  []byte
			progressTS   *time.Time
			isFavorite   bool
			isFavoriteTS *time.Time
			updatedAt    time.Time
			deletedAt    *time.Time
		)
		if err := rows.Scan(
			&id,
			&rating, &ratingTS,
			&readStatus, &readStatusTS,
			&progressRaw, &progressTS,
			&isFavorite, &isFavoriteTS,
			&updatedAt, &deletedAt,
		); err != nil {
			return nil, fmt.Errorf("sync ubi scan: %w", err)
		}

		// Tombstone short-circuit: per-field state is irrelevant once
		// the row is gone. Emit one op and move on.
		if deletedAt != nil && deletedAt.After(since) {
			ops = append(ops, responses.SyncOp{
				EntityType: "user_book_interaction",
				EntityID:   id,
				Deleted:    true,
				UpdatedAt:  *deletedAt,
			})
			continue
		}

		if ratingTS != nil && ratingTS.After(since) {
			var v any
			if rating != nil {
				v = *rating
			}
			ops = append(ops, responses.SyncOp{
				EntityType: "user_book_interaction",
				EntityID:   id,
				Field:      "rating",
				Value:      v,
				UpdatedAt:  *ratingTS,
			})
		}
		if readStatusTS != nil && readStatusTS.After(since) {
			ops = append(ops, responses.SyncOp{
				EntityType: "user_book_interaction",
				EntityID:   id,
				Field:      "read_status",
				Value:      readStatus,
				UpdatedAt:  *readStatusTS,
			})
		}
		if progressTS != nil && progressTS.After(since) {
			var v any
			if len(progressRaw) > 0 {
				v = json.RawMessage(progressRaw)
			}
			ops = append(ops, responses.SyncOp{
				EntityType: "user_book_interaction",
				EntityID:   id,
				Field:      "progress",
				Value:      v,
				UpdatedAt:  *progressTS,
			})
		}
		if isFavoriteTS != nil && isFavoriteTS.After(since) {
			ops = append(ops, responses.SyncOp{
				EntityType: "user_book_interaction",
				EntityID:   id,
				Field:      "is_favorite",
				Value:      isFavorite,
				UpdatedAt:  *isFavoriteTS,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sync ubi rows: %w", err)
	}
	return ops, nil
}

// ApplyUserBookInteractionOp applies a single op against
// user_book_interactions. The caller's user_id is enforced as the row
// owner; ops targeting other users' rows return not_found.
//
// Returns one of SyncApplyStatus* constants describing what happened.
func (r *SyncRepo) ApplyUserBookInteractionOp(ctx context.Context, userID uuid.UUID, op responses.SyncApplyOp) (string, error) {
	if op.EntityType != "user_book_interaction" {
		return SyncApplyStatusInvalid, nil
	}
	if op.Deleted {
		return r.applyUBITombstone(ctx, userID, op.EntityID, op.UpdatedAt)
	}
	switch op.Field {
	case "rating":
		var rating *int16
		if op.Value != nil {
			f, ok := op.Value.(float64)
			if !ok {
				return SyncApplyStatusInvalid, nil
			}
			n := int16(f)
			if n < 1 || n > 10 {
				return SyncApplyStatusInvalid, nil
			}
			rating = &n
		}
		return r.applyUBIField(ctx, userID, op.EntityID, op.UpdatedAt, "rating", "rating_updated_at", rating)
	case "read_status":
		s, ok := op.Value.(string)
		if !ok {
			return SyncApplyStatusInvalid, nil
		}
		return r.applyUBIField(ctx, userID, op.EntityID, op.UpdatedAt, "read_status", "read_status_updated_at", s)
	case "is_favorite":
		b, ok := op.Value.(bool)
		if !ok {
			return SyncApplyStatusInvalid, nil
		}
		return r.applyUBIField(ctx, userID, op.EntityID, op.UpdatedAt, "is_favorite", "is_favorite_updated_at", b)
	case "progress":
		var progressJSON interface{}
		if op.Value != nil {
			b, err := json.Marshal(op.Value)
			if err != nil {
				return SyncApplyStatusInvalid, nil
			}
			progressJSON = string(b)
		}
		return r.applyUBIField(ctx, userID, op.EntityID, op.UpdatedAt, "progress", "progress_updated_at", progressJSON)
	default:
		return SyncApplyStatusInvalid, nil
	}
}

// applyUBIField does the per-field LWW UPDATE: only writes if the
// server's timestamp for the field is older than the op's, or if the
// field has never been touched. Skipped writes are reported as
// discarded_stale unless the row doesn't exist at all (not_found).
func (r *SyncRepo) applyUBIField(ctx context.Context, userID, entityID uuid.UUID, opTS time.Time, valueCol, tsCol string, value interface{}) (string, error) {
	q := fmt.Sprintf(`
		UPDATE user_book_interactions
		   SET %s = $3, %s = $4, updated_at = NOW()
		 WHERE id = $1 AND user_id = $2
		   AND (%s IS NULL OR %s < $4)
		   AND deleted_at IS NULL
		 RETURNING id`,
		valueCol, tsCol, tsCol, tsCol,
	)
	var returned uuid.UUID
	err := r.db.QueryRow(ctx, q, entityID, userID, value, opTS).Scan(&returned)
	if err == nil {
		return SyncApplyStatusApplied, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("apply %s: %w", valueCol, err)
	}
	return r.classifyUBIMiss(ctx, userID, entityID)
}

// applyUBITombstone sets deleted_at if it's NULL or older than opTS.
// A row already deleted with a newer or equal timestamp is treated as
// discarded_stale (the existing deletion wins).
func (r *SyncRepo) applyUBITombstone(ctx context.Context, userID, entityID uuid.UUID, opTS time.Time) (string, error) {
	const q = `
		UPDATE user_book_interactions
		   SET deleted_at = $3, updated_at = NOW()
		 WHERE id = $1 AND user_id = $2
		   AND (deleted_at IS NULL OR deleted_at < $3)
		 RETURNING id`
	var returned uuid.UUID
	err := r.db.QueryRow(ctx, q, entityID, userID, opTS).Scan(&returned)
	if err == nil {
		return SyncApplyStatusApplied, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("apply tombstone: %w", err)
	}
	return r.classifyUBIMiss(ctx, userID, entityID)
}

// classifyUBIMiss runs after an apply UPDATE returned 0 rows. It
// distinguishes "row doesn't exist (or wrong owner)" from "row exists
// but the LWW comparison rejected the write."
func (r *SyncRepo) classifyUBIMiss(ctx context.Context, userID, entityID uuid.UUID) (string, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM user_book_interactions WHERE id = $1 AND user_id = $2)`
	var exists bool
	if err := r.db.QueryRow(ctx, q, entityID, userID).Scan(&exists); err != nil {
		return "", fmt.Errorf("classify miss: %w", err)
	}
	if !exists {
		return SyncApplyStatusNotFound, nil
	}
	return SyncApplyStatusDiscardedStale, nil
}
