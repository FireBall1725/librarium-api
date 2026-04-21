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

type UserAISettingsRepo struct {
	db *pgxpool.Pool
}

func NewUserAISettingsRepo(db *pgxpool.Pool) *UserAISettingsRepo {
	return &UserAISettingsRepo{db: db}
}

// UserAISettings is one row of user_ai_settings. TasteProfile is kept as
// opaque JSON so the form shape can evolve without schema churn.
type UserAISettings struct {
	UserID       uuid.UUID
	OptIn        bool
	TasteProfile json.RawMessage
}

// Get returns the user's saved AI settings. If the user has no row yet,
// returns the default: opted out, empty taste profile.
func (r *UserAISettingsRepo) Get(ctx context.Context, userID uuid.UUID) (*UserAISettings, error) {
	const q = `SELECT opt_in, taste_profile FROM user_ai_settings WHERE user_id = $1`
	s := &UserAISettings{UserID: userID, TasteProfile: json.RawMessage("{}")}
	err := r.db.QueryRow(ctx, q, userID).Scan(&s.OptIn, &s.TasteProfile)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("user_ai_settings get: %w", err)
	}
	if len(s.TasteProfile) == 0 {
		s.TasteProfile = json.RawMessage("{}")
	}
	return s, nil
}

// SetOptIn upserts the user's opt-in flag without touching taste_profile.
func (r *UserAISettingsRepo) SetOptIn(ctx context.Context, userID uuid.UUID, optIn bool) error {
	const q = `
		INSERT INTO user_ai_settings (user_id, opt_in, taste_profile)
		VALUES ($1, $2, '{}'::jsonb)
		ON CONFLICT (user_id) DO UPDATE SET opt_in = EXCLUDED.opt_in`
	if _, err := r.db.Exec(ctx, q, userID, optIn); err != nil {
		return fmt.Errorf("user_ai_settings set opt_in: %w", err)
	}
	return nil
}

// SetTasteProfile upserts the user's taste_profile JSON, creating the row with
// opt_in = false if it doesn't exist yet.
func (r *UserAISettingsRepo) SetTasteProfile(ctx context.Context, userID uuid.UUID, taste json.RawMessage) error {
	if len(taste) == 0 {
		taste = json.RawMessage("{}")
	}
	const q = `
		INSERT INTO user_ai_settings (user_id, opt_in, taste_profile)
		VALUES ($1, FALSE, $2::jsonb)
		ON CONFLICT (user_id) DO UPDATE SET taste_profile = EXCLUDED.taste_profile`
	if _, err := r.db.Exec(ctx, q, userID, []byte(taste)); err != nil {
		return fmt.Errorf("user_ai_settings set taste_profile: %w", err)
	}
	return nil
}
