// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PreferencesRepo struct {
	db *pgxpool.Pool
}

func NewPreferencesRepo(db *pgxpool.Pool) *PreferencesRepo {
	return &PreferencesRepo{db: db}
}

// Get returns the preferences map for the given user.
// Returns an empty map if no preferences have been saved yet.
func (r *PreferencesRepo) Get(ctx context.Context, userID uuid.UUID) (map[string]json.RawMessage, error) {
	const q = `SELECT prefs FROM user_preferences WHERE user_id = $1`
	var raw []byte
	err := r.db.QueryRow(ctx, q, userID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return make(map[string]json.RawMessage), nil
	}
	if err != nil {
		return nil, fmt.Errorf("preferences get: %w", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("preferences unmarshal: %w", err)
	}
	if out == nil {
		out = make(map[string]json.RawMessage)
	}
	return out, nil
}

// Merge merges the given key-value pairs into the user's stored preferences.
// Existing keys not present in patch are left unchanged.
func (r *PreferencesRepo) Merge(ctx context.Context, userID uuid.UUID, patch map[string]json.RawMessage) error {
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("preferences marshal patch: %w", err)
	}
	const q = `
		INSERT INTO user_preferences (user_id, prefs, updated_at)
		VALUES ($1, $2::jsonb, $3)
		ON CONFLICT (user_id) DO UPDATE
		SET prefs = user_preferences.prefs || EXCLUDED.prefs,
		    updated_at = EXCLUDED.updated_at`
	if _, err := r.db.Exec(ctx, q, userID, patchBytes, time.Now()); err != nil {
		return fmt.Errorf("preferences merge: %w", err)
	}
	return nil
}
