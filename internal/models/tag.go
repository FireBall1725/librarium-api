// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

// Tag represents a label that can be attached to books within a library.
// json tags are intentional — Tag is also embedded in Book via JSON agg.
type Tag struct {
	ID        uuid.UUID `json:"id"`
	LibraryID uuid.UUID `json:"library_id,omitempty"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}
