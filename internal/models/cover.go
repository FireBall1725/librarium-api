// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

type CoverImage struct {
	ID         uuid.UUID
	EntityType string
	EntityID   uuid.UUID
	Filename   string
	MimeType   string
	FileSize   int64
	IsPrimary  bool
	SourceURL  string
	CreatedBy  *uuid.UUID
	CreatedAt  time.Time
}
