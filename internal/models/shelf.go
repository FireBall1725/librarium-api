// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

type Shelf struct {
	ID           uuid.UUID
	LibraryID    uuid.UUID
	Name         string
	Description  string
	Color        string
	Icon         string
	DisplayOrder int
	BookCount    int // populated by List query, not a DB column
	Tags         []*Tag
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
