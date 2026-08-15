// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package jobs

import (
	"encoding/json"
	"testing"
)

func TestParseRetentionConfigDefaults(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"empty":   nil,
		"object":  json.RawMessage(`{}`),
		"garbage": json.RawMessage(`not json`),
	} {
		t.Run(name, func(t *testing.T) {
			opts := ParseRetentionConfig(raw)
			if opts.MaxAgeDays != DefaultRetentionMaxAgeDays {
				t.Errorf("max age = %d, want %d", opts.MaxAgeDays, DefaultRetentionMaxAgeDays)
			}
			if opts.MaxPerKind != DefaultRetentionMaxPerKind {
				t.Errorf("max per kind = %d, want %d", opts.MaxPerKind, DefaultRetentionMaxPerKind)
			}
		})
	}
}

func TestParseRetentionConfigExplicitValues(t *testing.T) {
	opts := ParseRetentionConfig(json.RawMessage(`{"max_age_days":7,"max_per_kind":50}`))
	if opts.MaxAgeDays != 7 {
		t.Errorf("max age = %d, want 7", opts.MaxAgeDays)
	}
	if opts.MaxPerKind != 50 {
		t.Errorf("max per kind = %d, want 50", opts.MaxPerKind)
	}
}

// An explicit 0 turns one limit off — count-only retention is a valid
// setup and must not silently revert to the 30-day default.
func TestParseRetentionConfigZeroDisablesOneLimit(t *testing.T) {
	opts := ParseRetentionConfig(json.RawMessage(`{"max_age_days":0,"max_per_kind":500}`))
	if opts.MaxAgeDays != 0 {
		t.Errorf("max age = %d, want 0", opts.MaxAgeDays)
	}
	if opts.MaxPerKind != 500 {
		t.Errorf("max per kind = %d, want 500", opts.MaxPerKind)
	}
}

// A partial config keeps the default for the key it left out.
func TestParseRetentionConfigPartial(t *testing.T) {
	opts := ParseRetentionConfig(json.RawMessage(`{"max_age_days":90}`))
	if opts.MaxAgeDays != 90 {
		t.Errorf("max age = %d, want 90", opts.MaxAgeDays)
	}
	if opts.MaxPerKind != DefaultRetentionMaxPerKind {
		t.Errorf("max per kind = %d, want %d", opts.MaxPerKind, DefaultRetentionMaxPerKind)
	}
}

func TestParseRetentionConfigClamps(t *testing.T) {
	opts := ParseRetentionConfig(json.RawMessage(`{"max_age_days":-1,"max_per_kind":99999999}`))
	if opts.MaxAgeDays != 0 {
		t.Errorf("negative age = %d, want 0", opts.MaxAgeDays)
	}
	if opts.MaxPerKind != maxRetentionPerKind {
		t.Errorf("max per kind = %d, want %d", opts.MaxPerKind, maxRetentionPerKind)
	}
}
