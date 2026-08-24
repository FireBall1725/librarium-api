// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// UserBook is what one person thinks of one work.
//
// Keyed to the WORK, not the edition, which is the change everything else in
// the redesign hangs off. Starring the paperback no longer leaves the hardcover
// unstarred, every read path stops collapsing per-edition rows into one answer,
// and a book with no edition at all can carry a rating, which the old shape
// forbade outright. Borrowing a book, rating it, and buying it later becomes a
// thing the schema can express.
type UserBook struct {
	UserID uuid.UUID `json:"user_id"`
	BookID uuid.UUID `json:"book_id"`

	ReadStatus string `json:"read_status"`
	Rating     *int   `json:"rating,omitempty"`
	IsFavorite bool   `json:"is_favorite"`
	Review     string `json:"review"`
	Notes      string `json:"notes"`
	// Wants marks a catalogued book as wanted. Uncatalogued wants live in
	// wishlist with free text instead, and both are read through the same list.
	Wants bool `json:"wants,omitempty"`

	// Per-field timestamps exist for last-writer-wins sync with iOS, which is
	// one client talking to several servers rather than servers talking to each
	// other.
	ReadStatusUpdatedAt *time.Time `json:"read_status_updated_at,omitempty"`
	RatingUpdatedAt     *time.Time `json:"rating_updated_at,omitempty"`
	IsFavoriteUpdatedAt *time.Time `json:"is_favorite_updated_at,omitempty"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"-"`

	// Inherited is true when the status was not set directly but comes from a
	// container this person has read: mark the 3-in-1 read and volume 2 reads
	// as read. Filled by reads that go through effective_read_status.
	Inherited bool `json:"inherited,omitempty"`
}

// ReadingSession is one pass through a work.
//
// user_books is the verdict; this is the log. Marking a book read without ever
// logging a session is the common case and stays possible. A reread is a second
// row rather than an overwrite, which is what reread_count was standing in for.
type ReadingSession struct {
	ID     uuid.UUID `json:"id"`
	UserID uuid.UUID `json:"user_id"`
	BookID uuid.UUID `json:"book_id"`
	// EditionID says which printing was read, so a page number means a page of
	// that printing. The database guarantees it belongs to BookID.
	EditionID *uuid.UUID `json:"edition_id,omitempty"`

	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Status     string     `json:"status"`

	// Progress is typed rather than a blob, so it can be compared, sorted and
	// aggregated instead of every reader guessing at a shape nobody validates.
	// A page unit requires an edition: a page number is meaningless without
	// knowing which printing it counts.
	ProgressUnit  string   `json:"progress_unit"`
	ProgressValue *float64 `json:"progress_value,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// List is a named set of works: a shelf someone filled by hand, or a saved
// filter that fills itself.
//
// Shelves and saved views were never distinguished by ownership but by how
// membership is decided, so they are one concept with a kind. That gives one
// sharing implementation rather than two, which matters because a public link
// has to work the same way for both.
type List struct {
	ID          uuid.UUID `json:"id"`
	OwnerUserID uuid.UUID `json:"owner_user_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Icon        string    `json:"icon"`
	Color       string    `json:"color"`

	// Kind is manual or smart. A manual list enumerates its books; a smart one
	// computes them from Filter.
	Kind string `json:"kind"`
	// Filter is the stored query for a smart list. FilterVersion is not
	// decoration: a stored filter is a query language with no schema, and
	// unversioned it gets silently reinterpreted when the vocabulary changes.
	// json.RawMessage, not []byte: a []byte marshals to base64, so the filter
	// would reach a client as an opaque string it has to decode before it can
	// read the query it wrote.
	Filter        json.RawMessage `json:"filter"`
	FilterVersion *int            `json:"filter_version,omitempty"`

	Layout       string `json:"layout"`
	DisplayOrder int    `json:"display_order"`

	// Visibility is private, library or public. shared_library_id is set
	// exactly when library, share_token exactly when public, both enforced by
	// the database so the three cannot disagree.
	Visibility      string     `json:"visibility"`
	SharedLibraryID *uuid.UUID `json:"shared_library_id,omitempty"`
	ShareToken      string     `json:"share_token"`

	// BuiltinKey names which shipped list this is, empty for one a person made.
	// Hidden and Permanent are looked up from that key rather than stored, so a
	// rename cannot make the default list deletable and a later release can
	// change its mind without touching rows.
	BuiltinKey string `json:"builtin_key,omitempty"`
	Hidden     bool   `json:"hidden,omitempty"`
	Permanent  bool   `json:"permanent,omitempty"`

	BookCount int       `json:"book_count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WishlistEntry is something someone wants.
//
// One table, not two. It points at a catalogue entry when there is one and
// carries free text when there is not, so "show me my wishlist" stops being a
// union of two shapes. Finding the book later sets BookID and clears the text
// without the row changing meaning.
type WishlistEntry struct {
	ID     uuid.UUID  `json:"id"`
	UserID uuid.UUID  `json:"user_id"`
	BookID *uuid.UUID `json:"book_id"`

	Title      string `json:"title"`
	AuthorName string `json:"author_name"`
	Notes      string `json:"notes"`
	Priority   int    `json:"priority"`

	CreatedAt time.Time `json:"created_at"`
}
