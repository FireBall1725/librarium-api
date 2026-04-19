// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

type MediaType struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description,omitempty"`
	BookCount   int       `json:"book_count"`
}

// BookContributor is used both as a DB scan target (via JSON from json_agg)
// and as an API response value — json tags are intentional here.
type BookContributor struct {
	ContributorID uuid.UUID `json:"contributor_id"`
	Name          string    `json:"name"`
	Role          string    `json:"role"`
	DisplayOrder  int       `json:"display_order"`
}

// BookShelfRef is a lightweight reference to a shelf this book belongs to.
type BookShelfRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type Book struct {
	ID           uuid.UUID
	LibraryID    uuid.UUID
	Title        string
	Subtitle     string
	MediaTypeID  uuid.UUID
	MediaType    string // display_name from joined media_types row
	Description  string
	AddedBy      *uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Contributors []BookContributor
	Tags         []Tag
	Genres       []Genre
	HasCover     bool
	Series       []BookSeriesRef
	Shelves      []BookShelfRef
	Publisher      string
	PublishYear    *int
	Language       string
	UserReadStatus string
}
