// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type IdentityRepo struct {
	db *pgxpool.Pool
}

func NewIdentityRepo(db *pgxpool.Pool) *IdentityRepo {
	return &IdentityRepo{db: db}
}

type localCredentials struct {
	PasswordHash string `json:"password_hash"`
}

// CreateLocal inserts a local (password) identity for a user within a transaction.
func (r *IdentityRepo) CreateLocal(ctx context.Context, tx pgx.Tx, id, userID uuid.UUID, username, passwordHash string) error {
	creds, err := json.Marshal(localCredentials{PasswordHash: passwordHash})
	if err != nil {
		return fmt.Errorf("marshalling credentials: %w", err)
	}
	const q = `
		INSERT INTO user_identities (id, user_id, provider, provider_user_id, credentials)
		VALUES ($1, $2, 'local', $3, $4)`
	if _, err := tx.Exec(ctx, q, id, userID, username, creds); err != nil {
		return fmt.Errorf("inserting local identity: %w", err)
	}
	return nil
}

// UpdateLocalPassword replaces the bcrypt hash for a user's local identity.
func (r *IdentityRepo) UpdateLocalPassword(ctx context.Context, userID uuid.UUID, newHash string) error {
	creds, err := json.Marshal(localCredentials{PasswordHash: newHash})
	if err != nil {
		return fmt.Errorf("marshalling credentials: %w", err)
	}
	const q = `UPDATE user_identities SET credentials = $2 WHERE user_id = $1 AND provider = 'local'`
	if _, err := r.db.Exec(ctx, q, userID, creds); err != nil {
		return fmt.Errorf("updating local password: %w", err)
	}
	return nil
}

// GetLocalPasswordHash returns the bcrypt hash stored for a user's local identity.
func (r *IdentityRepo) GetLocalPasswordHash(ctx context.Context, userID uuid.UUID) (string, error) {
	var credsJSON []byte
	const q = `SELECT credentials FROM user_identities WHERE user_id = $1 AND provider = 'local'`
	err := r.db.QueryRow(ctx, q, userID).Scan(&credsJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("getting local credentials: %w", err)
	}
	var creds localCredentials
	if err := json.Unmarshal(credsJSON, &creds); err != nil {
		return "", fmt.Errorf("unmarshalling credentials: %w", err)
	}
	return creds.PasswordHash, nil
}
