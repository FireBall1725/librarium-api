// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fireball1725/librarium-api/internal/repository"
)

// Configurable jobs are persisted under instance_settings with this prefix.
// Key format: job:<id>   JSON matching the job's config struct.
const settingsJobPrefix = "job:"

// JobID is the canonical identifier for a configurable job. Kept open-ended so
// future jobs (release-check cadence, metadata-refresh cadence, etc.) can drop
// in alongside ai-suggestions.
type JobID = string

const (
	JobAISuggestions JobID = "ai-suggestions"
)

// AISuggestionsJobConfig is the persisted config for the AI suggestions job.
// Mirrors the plan in plans/ai-suggestions.md:
//   - Enabled/disabled
//   - Cadence (minutes between runs; 0 = disabled regardless of Enabled)
//   - Max buy / read_next suggestions per user per run
//   - Include taste profile toggle
type AISuggestionsJobConfig struct {
	Enabled                bool `json:"enabled"`
	IntervalMinutes        int  `json:"interval_minutes"`
	MaxBuyPerUser          int  `json:"max_buy_per_user"`
	MaxReadNextPerUser     int  `json:"max_read_next_per_user"`
	IncludeTasteProfile    bool `json:"include_taste_profile"`
	UserRunRateLimitPerDay int  `json:"user_run_rate_limit_per_day"`
}

// defaultAISuggestionsJobConfig is what a fresh install sees before the admin
// saves anything. Chosen to be safe (off by default) while telegraphing a
// reasonable cadence and cap for when admin enables it.
var defaultAISuggestionsJobConfig = AISuggestionsJobConfig{
	Enabled:                false,
	IntervalMinutes:        3 * 24 * 60, // every 3 days
	MaxBuyPerUser:          5,
	MaxReadNextPerUser:     5,
	IncludeTasteProfile:    true,
	UserRunRateLimitPerDay: 1,
}

// JobSummary is what GET /admin/jobs returns: enough to render the jobs list
// UI without pulling every job's full config. Kind distinguishes "scheduled"
// (cadence-driven, configurable) from "queue" (already-listed import/enrichment
// jobs, left untouched).
type JobSummary struct {
	ID          JobID  `json:"id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Kind        string `json:"kind"` // "scheduled" for now
	Enabled     bool   `json:"enabled"`
}

// JobService reads and writes configurable-job settings from instance_settings.
// Intentionally does not own job execution — the worker (M6) pulls configs via
// this service to decide when to fire.
type JobService struct {
	settings *repository.SettingsRepo
}

func NewJobService(settings *repository.SettingsRepo) *JobService {
	return &JobService{settings: settings}
}

// ListJobs returns a summary of every known configurable job. Today that's
// just ai-suggestions; more land as the jobs framework grows.
func (s *JobService) ListJobs(ctx context.Context) ([]JobSummary, error) {
	ai, err := s.GetAISuggestionsConfig(ctx)
	if err != nil {
		return nil, err
	}
	return []JobSummary{
		{
			ID:          JobAISuggestions,
			DisplayName: "AI suggestions",
			Description: "Generates per-user book suggestions using the active AI provider.",
			Kind:        "scheduled",
			Enabled:     ai.Enabled,
		},
	}, nil
}

// GetAISuggestionsConfig returns the saved config, or the default if nothing
// has been saved yet.
func (s *JobService) GetAISuggestionsConfig(ctx context.Context) (AISuggestionsJobConfig, error) {
	raw, err := s.settings.Get(ctx, settingsJobPrefix+JobAISuggestions)
	if errors.Is(err, repository.ErrNotFound) {
		return defaultAISuggestionsJobConfig, nil
	}
	if err != nil {
		return defaultAISuggestionsJobConfig, err
	}
	cfg := defaultAISuggestionsJobConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return defaultAISuggestionsJobConfig, fmt.Errorf("decode ai-suggestions job config: %w", err)
	}
	return cfg, nil
}

// SetAISuggestionsConfig validates and persists the AI suggestions config.
func (s *JobService) SetAISuggestionsConfig(ctx context.Context, cfg AISuggestionsJobConfig) error {
	if cfg.IntervalMinutes < 0 {
		return fmt.Errorf("interval_minutes must be >= 0")
	}
	if cfg.MaxBuyPerUser < 0 || cfg.MaxReadNextPerUser < 0 {
		return fmt.Errorf("per-user caps must be >= 0")
	}
	if cfg.UserRunRateLimitPerDay < 0 {
		return fmt.Errorf("user_run_rate_limit_per_day must be >= 0")
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, settingsJobPrefix+JobAISuggestions, string(data))
}
