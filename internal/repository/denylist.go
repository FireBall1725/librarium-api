// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DenylistRepo struct {
	db *pgxpool.Pool
}

func NewDenylistRepo(db *pgxpool.Pool) *DenylistRepo {
	return &DenylistRepo{db: db}
}

// Add records a revoked access token JTI. ON CONFLICT DO NOTHING is safe
// because a JTI is a UUID — collisions are impossible in practice.
func (r *DenylistRepo) Add(ctx context.Context, jti uuid.UUID, expiresAt time.Time) error {
	const q = `INSERT INTO revoked_access_tokens (jti, expires_at) VALUES ($1, $2) ON CONFLICT DO NOTHING`
	if _, err := r.db.Exec(ctx, q, jti, expiresAt); err != nil {
		return fmt.Errorf("adding to denylist: %w", err)
	}
	return nil
}

// IsRevoked returns true if the JTI is in the denylist and has not yet expired.
func (r *DenylistRepo) IsRevoked(ctx context.Context, jti uuid.UUID) (bool, error) {
	var exists bool
	const q = `SELECT EXISTS(SELECT 1 FROM revoked_access_tokens WHERE jti = $1 AND expires_at > NOW())`
	if err := r.db.QueryRow(ctx, q, jti).Scan(&exists); err != nil {
		return false, fmt.Errorf("checking denylist: %w", err)
	}
	return exists, nil
}

// DeleteExpired removes entries whose access tokens have already expired.
// Safe to run on a schedule; rows are tiny so a full table scan is fine at small scale.
func (r *DenylistRepo) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM revoked_access_tokens WHERE expires_at <= NOW()`)
	if err != nil {
		return 0, fmt.Errorf("deleting expired denylist entries: %w", err)
	}
	return tag.RowsAffected(), nil
}
