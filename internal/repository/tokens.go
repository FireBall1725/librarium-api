// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TokenRepo struct {
	db *pgxpool.Pool
}

func NewTokenRepo(db *pgxpool.Pool) *TokenRepo {
	return &TokenRepo{db: db}
}

func (r *TokenRepo) Create(ctx context.Context, id, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	const q = `
		INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)`
	if _, err := r.db.Exec(ctx, q, id, userID, tokenHash, expiresAt); err != nil {
		return fmt.Errorf("storing refresh token: %w", err)
	}
	return nil
}

func (r *TokenRepo) FindByHash(ctx context.Context, tokenHash string) (*models.RefreshToken, error) {
	const q = `
		SELECT id, user_id, token_hash, expires_at, created_at, revoked_at
		FROM refresh_tokens WHERE token_hash = $1`

	var (
		pgID      pgtype.UUID
		pgUserID  pgtype.UUID
		hash      string
		expiresAt pgtype.Timestamptz
		createdAt pgtype.Timestamptz
		revokedAt pgtype.Timestamptz
	)
	err := r.db.QueryRow(ctx, q, tokenHash).Scan(&pgID, &pgUserID, &hash, &expiresAt, &createdAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding refresh token: %w", err)
	}

	t := &models.RefreshToken{
		ID:        uuid.UUID(pgID.Bytes),
		UserID:    uuid.UUID(pgUserID.Bytes),
		TokenHash: hash,
		ExpiresAt: expiresAt.Time,
		CreatedAt: createdAt.Time,
	}
	if revokedAt.Valid {
		rv := revokedAt.Time
		t.RevokedAt = &rv
	}
	return t, nil
}

func (r *TokenRepo) Revoke(ctx context.Context, tokenHash string) error {
	const q = `UPDATE refresh_tokens SET revoked_at = NOW() WHERE token_hash = $1 AND revoked_at IS NULL`
	if _, err := r.db.Exec(ctx, q, tokenHash); err != nil {
		return fmt.Errorf("revoking token: %w", err)
	}
	return nil
}

func (r *TokenRepo) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	const q = `UPDATE refresh_tokens SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`
	if _, err := r.db.Exec(ctx, q, userID); err != nil {
		return fmt.Errorf("revoking all tokens for user: %w", err)
	}
	return nil
}
