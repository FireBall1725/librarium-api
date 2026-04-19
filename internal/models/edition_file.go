// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

type EditionFile struct {
	ID                uuid.UUID
	EditionID         uuid.UUID
	FileFormat        string
	FileName          string
	FilePath          string
	StorageLocationID *uuid.UUID
	FileSize          *int64
	DisplayOrder      int
	CreatedAt         time.Time

	// RootPath is the base directory for this file (not stored in DB).
	// Populated by the service layer after loading from the repository.
	RootPath string
}
