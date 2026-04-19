// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"encoding/json"

	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

// AIUserService covers the per-user AI settings: opt-in and taste profile.
// Admin-side config lives on AIService; these two concerns are kept apart
// because their callers (admin vs /me endpoints) authorize differently.
type AIUserService struct {
	repo *repository.UserAISettingsRepo
}

func NewAIUserService(repo *repository.UserAISettingsRepo) *AIUserService {
	return &AIUserService{repo: repo}
}

// UserAIPrefsView is what the /me/ai-prefs endpoint returns. Keeping the DTO
// small now so we can add fields (e.g. last-run timestamp) without breaking
// the response contract.
type UserAIPrefsView struct {
	OptIn bool `json:"opt_in"`
}

// GetPrefs returns the user's opt-in state. New users default to opt_in=false.
func (s *AIUserService) GetPrefs(ctx context.Context, userID uuid.UUID) (UserAIPrefsView, error) {
	settings, err := s.repo.Get(ctx, userID)
	if err != nil {
		return UserAIPrefsView{}, err
	}
	return UserAIPrefsView{OptIn: settings.OptIn}, nil
}

// SetOptIn updates the user's opt-in flag.
func (s *AIUserService) SetOptIn(ctx context.Context, userID uuid.UUID, optIn bool) error {
	return s.repo.SetOptIn(ctx, userID, optIn)
}

// GetTasteProfile returns the raw JSON taste profile. Callers forward this
// straight to the client; the shape is validated on write.
func (s *AIUserService) GetTasteProfile(ctx context.Context, userID uuid.UUID) (json.RawMessage, error) {
	settings, err := s.repo.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	return settings.TasteProfile, nil
}

// SetTasteProfile stores an arbitrary JSON object as the user's taste profile.
// The shape is intentionally loose: the taste profile form evolves
// independently of the DB schema, and the worker knows how to render whatever
// keys are present.
func (s *AIUserService) SetTasteProfile(ctx context.Context, userID uuid.UUID, taste json.RawMessage) error {
	// Enforce that the top-level value is an object, not a primitive or array.
	// Anything else would break prompt rendering.
	var probe map[string]any
	if err := json.Unmarshal(taste, &probe); err != nil {
		return err
	}
	return s.repo.SetTasteProfile(ctx, userID, taste)
}
