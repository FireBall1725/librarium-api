// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

type Library struct {
	ID          uuid.UUID
	Name        string
	Description string
	Slug        string
	OwnerID     uuid.UUID
	IsPublic    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type LibraryMember struct {
	UserID      uuid.UUID
	Username    string
	DisplayName string
	Email       string
	RoleID      uuid.UUID
	RoleName    string
	InvitedBy   *uuid.UUID
	JoinedAt    time.Time
	Tags        []*Tag
}
