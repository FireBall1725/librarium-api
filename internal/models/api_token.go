// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

// APIToken is a personal access token minted by a user for scripted /
// machine access to the API. The raw token value is shown exactly once at
// creation; this row stores only a sha256 hash plus the last four chars of
// the raw value for UI disambiguation.
//
// Scopes cap what the token can do: an empty slice means "inherit the user's
// full permissions" (classic PAT behaviour), a non-empty slice is
// AND-intersected with the user's permissions at every permission check.
type APIToken struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Name        string
	TokenHash   string
	TokenSuffix string
	Scopes      []string
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
}

// Active returns true if the token can be used right now: not revoked and
// not past its expiry (if an expiry was set).
func (t *APIToken) Active(now time.Time) bool {
	if t.RevokedAt != nil {
		return false
	}
	if t.ExpiresAt != nil && !t.ExpiresAt.After(now) {
		return false
	}
	return true
}
