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

// Book represents a work — the abstract creative entity. Library ownership
// is expressed via the library_books junction (see LibraryBook); a book row
// no longer carries a single library_id.
type Book struct {
	ID           uuid.UUID
	Title        string
	Subtitle     string
	MediaTypeID  uuid.UUID
	MediaType    string // display_name from joined media_types row
	Description  string
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
	// UserRating is the calling user's rating (1-10 half-star integer; 0
	// means no rating). Picked from the same interaction row as
	// UserReadStatus so multi-edition books stay consistent.
	UserRating int
	// UserProgressPct is the calling user's reading progress as a percent
	// (0-100). Pulled from progress.percent on the chosen interaction;
	// zero when there's no progress row or no percent set.
	UserProgressPct float64
	// ActiveLoanCount is the number of active (not yet returned) loans for
	// this book. Scoped to a single library when the read is library-scoped
	// (ListBooks via library), or counted across every library otherwise.
	// Drives the "loaned out" badge on book rows without forcing the client
	// to fetch loans separately.
	ActiveLoanCount int
	// Libraries is the set of libraries holding this book (populated by the
	// service layer on reads that need it; empty when the book is floating).
	Libraries []BookLibraryRef
}

// BookLibraryRef is a lightweight reference to a library that holds this book.
type BookLibraryRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// LibraryBook is a row in the library_books junction — "library X holds book Y".
type LibraryBook struct {
	ID        uuid.UUID
	LibraryID uuid.UUID
	BookID    uuid.UUID
	AddedBy   *uuid.UUID
	AddedAt   time.Time
}
