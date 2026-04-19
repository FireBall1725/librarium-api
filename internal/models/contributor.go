// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

// Contributor is a person (author, narrator, illustrator, etc.) associated with books.
// The profile fields (Bio, BornDate, etc.) are stored in the contributors table but
// are only populated by the service layer when doing a full fetch.
type Contributor struct {
	ID          uuid.UUID
	Name        string
	SortName    string // library-style sort key, e.g. "Gaiman, Neil"; derived if not user-set
	IsCorporate bool   // true for publishers/studios — skip name-inversion in UI
	Bio         string
	BornDate    *time.Time
	DiedDate    *time.Time
	Nationality string
	ExternalIDs map[string]string // from JSONB column; keys e.g. "openlibrary", "hardcover"
	HasPhoto    bool              // true when a cover_images row exists for this contributor
	BookCount   int               // computed: books in a given library context
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ContributorWork is one entry in a contributor's bibliography, sourced from a provider.
// Users may soft-delete entries they don't want shown.
type ContributorWork struct {
	ID            uuid.UUID
	ContributorID uuid.UUID
	Title         string
	ISBN13        string
	ISBN10        string
	PublishYear   *int
	CoverURL      string
	Source        string
	DeletedAt     *time.Time
	CreatedAt     time.Time

	// Computed fields — not stored in DB, populated by service layer.
	InLibrary     bool
	LibraryBookID *uuid.UUID // set when InLibrary is true
}
