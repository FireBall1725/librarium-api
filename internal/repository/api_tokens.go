// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// APITokenPrefix is the identifying prefix for every personal access token
// the server issues. Kept as a constant so grep / secret-scanning tooling
// has one canonical value to match.
const APITokenPrefix = "lbrm_pat_"

// apiTokenRandomLen is the number of base62 characters drawn from random
// bytes for the secret body. 43 chars ≈ 256 bits of entropy.
const apiTokenRandomLen = 43

type APITokenRepo struct {
	db *pgxpool.Pool
}

func NewAPITokenRepo(db *pgxpool.Pool) *APITokenRepo {
	return &APITokenRepo{db: db}
}

// NewToken is the result of minting a token. `Raw` is the only time the
// full credential is ever visible — it's returned once in the create
// response and never again. `Token` is the stored row (without the raw
// value) for the caller to persist and return metadata.
type NewToken struct {
	Raw   string
	Token *models.APIToken
}

// Generate mints a fresh raw token and its stored row. Does not insert into
// the DB — call Create with the NewToken value.
func Generate(userID uuid.UUID, name string, scopes []string, expiresAt *time.Time) (*NewToken, error) {
	body, err := randomBase62(apiTokenRandomLen)
	if err != nil {
		return nil, fmt.Errorf("random token body: %w", err)
	}
	raw := APITokenPrefix + body
	hash := sha256.Sum256([]byte(raw))
	suffix := raw[len(raw)-4:]

	if scopes == nil {
		scopes = []string{}
	}

	tok := &models.APIToken{
		ID:          uuid.New(),
		UserID:      userID,
		Name:        name,
		TokenHash:   hex.EncodeToString(hash[:]),
		TokenSuffix: suffix,
		Scopes:      scopes,
		ExpiresAt:   expiresAt,
		CreatedAt:   time.Now().UTC(),
	}
	return &NewToken{Raw: raw, Token: tok}, nil
}

// HashToken computes the storage hash for a raw token value. Used by the
// auth middleware to look up an incoming header without trusting the client.
func HashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// Create inserts a newly-minted token. The caller is responsible for also
// returning the raw value to the client — the repo never re-reads it.
func (r *APITokenRepo) Create(ctx context.Context, t *models.APIToken) error {
	const q = `
		INSERT INTO api_tokens (id, user_id, name, token_hash, token_suffix, scopes, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := r.db.Exec(ctx, q,
		t.ID, t.UserID, t.Name, t.TokenHash, t.TokenSuffix, t.Scopes, t.ExpiresAt, t.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting api token: %w", err)
	}
	return nil
}

// ListByUser returns the caller's tokens, newest first, including already-
// revoked rows so the UI can show a full history if it wants. Callers that
// only want active tokens should filter on RevokedAt == nil.
func (r *APITokenRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]*models.APIToken, error) {
	const q = `
		SELECT id, user_id, name, token_hash, token_suffix, scopes,
		       last_used_at, expires_at, revoked_at, created_at
		  FROM api_tokens
		 WHERE user_id = $1
		 ORDER BY created_at DESC`
	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("listing api tokens: %w", err)
	}
	defer rows.Close()

	var out []*models.APIToken
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// FindActiveByHash is the hot path for auth middleware. Returns the token
// iff it exists, isn't revoked, and (if an expiry is set) isn't past it.
func (r *APITokenRepo) FindActiveByHash(ctx context.Context, hash string) (*models.APIToken, error) {
	const q = `
		SELECT id, user_id, name, token_hash, token_suffix, scopes,
		       last_used_at, expires_at, revoked_at, created_at
		  FROM api_tokens
		 WHERE token_hash = $1
		   AND revoked_at IS NULL
		   AND (expires_at IS NULL OR expires_at > NOW())`
	row := r.db.QueryRow(ctx, q, hash)
	t, err := scanToken(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// TouchLastUsed updates the token's last_used_at to now. Fire-and-forget
// from the auth middleware so request latency isn't blocked on the write.
// Silently swallows errors for the same reason.
func (r *APITokenRepo) TouchLastUsed(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE api_tokens SET last_used_at = NOW() WHERE id = $1`, id)
	return err
}

// IsInstanceAdmin is a small pointer lookup used by the PAT auth path to
// populate UserClaims.IsInstanceAdmin without pulling in the full UserRepo
// dependency. Keeps the middleware's package surface tight.
func (r *APITokenRepo) IsInstanceAdmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	var admin bool
	if err := r.db.QueryRow(ctx,
		`SELECT is_instance_admin FROM users WHERE id = $1`, userID,
	).Scan(&admin); err != nil {
		return false, fmt.Errorf("loading user admin flag: %w", err)
	}
	return admin, nil
}

// Revoke soft-deletes a token by setting revoked_at. Scoped by user_id so
// one user can't revoke another user's token by id.
func (r *APITokenRepo) Revoke(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE api_tokens SET revoked_at = NOW()
		  WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		id, userID)
	if err != nil {
		return fmt.Errorf("revoking api token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func scanToken(s scanner) (*models.APIToken, error) {
	t := &models.APIToken{}
	if err := s.Scan(
		&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.TokenSuffix, &t.Scopes,
		&t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("scanning api token: %w", err)
	}
	return t, nil
}

// randomBase62 returns a cryptographically-random string of the given length
// using base62 alphabet. Uses crypto/rand for entropy; math/big is only used
// as a convenient way to index into the alphabet.
func randomBase62(n int) (string, error) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	max := big.NewInt(int64(len(alphabet)))
	buf := make([]byte, n)
	for i := range n {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = alphabet[idx.Int64()]
	}
	return string(buf), nil
}
