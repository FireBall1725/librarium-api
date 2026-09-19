// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package models

import (
	"time"

	"github.com/google/uuid"
)

// Kiosk is an iPad on a library's wall. It signs in with its own scoped API
// token; these are its settings.
type Kiosk struct {
	ID             uuid.UUID  `json:"id"`
	LibraryID      uuid.UUID  `json:"library_id"`
	Name           string     `json:"name"`
	Clock24h       *bool      `json:"clock_24h"` // null follows the iPad's region
	AllowAnonymous bool       `json:"allow_anonymous"`
	AllowSignup    bool       `json:"allow_signup"`
	ShowBorrower   bool       `json:"show_borrower"`
	IdleSeconds    int        `json:"idle_seconds"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// KioskMember is one entry in a kiosk's "who are you" picker.
type KioskMember struct {
	UserID      uuid.UUID `json:"user_id"`
	DisplayName string    `json:"display_name"`
	HasPIN      bool      `json:"has_pin"`
}
