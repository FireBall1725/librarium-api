// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

type StorageLocation struct {
	ID           uuid.UUID
	LibraryID    uuid.UUID
	Name         string
	RootPath     string
	MediaFormat  string
	PathTemplate string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
