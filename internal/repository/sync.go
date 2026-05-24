// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/responses"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
			id             uuid.UUID
			rating         *int16
			ratingTS       *time.Time
			readStatus     string
			readStatusTS   *time.Time
			progressRaw    []byte
			progressTS     *time.Time
			isFavorite     bool
			isFavoriteTS   *time.Time
			updatedAt      time.Time
			deletedAt      *time.Time
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
