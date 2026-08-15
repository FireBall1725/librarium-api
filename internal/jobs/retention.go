// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package jobs

import (
	"encoding/json"

	"github.com/fireball1725/librarium-api/internal/repository"
)

// Retention defaults for the history_prune kind. Thirty days is longer
// than anyone spends debugging a failed import, and 200 rows per kind
// keeps the history page useful on an instance that runs a job every few
// minutes.
const (
	DefaultRetentionMaxAgeDays = 30
	DefaultRetentionMaxPerKind = 200
)

// Bounds on what an admin can save. The upper limits exist so a typo in
// the config editor can't turn retention into a no-op that nobody
// notices for a year.
const (
	maxRetentionAgeDays = 3650
	maxRetentionPerKind = 100000
)

// RetentionConfig is the job_schedules.config shape for history_prune.
// Pointers so an absent key falls back to the default while an explicit
// 0 means "don't apply this limit" — an admin who wants pure count-based
// retention writes {"max_age_days": 0, "max_per_kind": 500}.
type RetentionConfig struct {
	MaxAgeDays *int `json:"max_age_days,omitempty"`
	MaxPerKind *int `json:"max_per_kind,omitempty"`
}

// ParseRetentionConfig turns a schedule row's config blob into prune
// options. Malformed JSON falls back to the defaults rather than
// erroring — a bad config should not stop retention from running, or the
// table it protects grows while somebody works out what's wrong with it.
func ParseRetentionConfig(raw json.RawMessage) repository.PruneOpts {
	opts := repository.PruneOpts{
		MaxAgeDays: DefaultRetentionMaxAgeDays,
		MaxPerKind: DefaultRetentionMaxPerKind,
	}
	if len(raw) == 0 {
		return opts
	}
	var cfg RetentionConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return opts
	}
	if cfg.MaxAgeDays != nil {
		opts.MaxAgeDays = clampRetention(*cfg.MaxAgeDays, maxRetentionAgeDays)
	}
	if cfg.MaxPerKind != nil {
		opts.MaxPerKind = clampRetention(*cfg.MaxPerKind, maxRetentionPerKind)
	}
	return opts
}

// clampRetention folds a negative value to 0 (limit disabled) and caps
// anything above the ceiling.
func clampRetention(v, ceiling int) int {
	if v < 0 {
		return 0
	}
	if v > ceiling {
		return ceiling
	}
	return v
}
