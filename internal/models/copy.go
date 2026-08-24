// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

// Copy is one physical object a library holds.
//
// One row per object, which is what library_book_editions.copy_count could
// never express: a counter cannot say that one of your two copies is signed and
// the other is a reading copy in the office.
//
// There is deliberately no status field. A row means the object is here.
// Something on order is a wishlist entry until it arrives, because a row that
// also means "not yet" is a row meaning two things.
type Copy struct {
	ID        uuid.UUID `json:"id"`
	LibraryID uuid.UUID `json:"library_id"`
	BookID    uuid.UUID `json:"book_id"`
	// EditionID is nil when the printing was never recorded, which is a
	// supported state rather than a gap. When set, the database guarantees it
	// is a printing of BookID.
	EditionID *uuid.UUID `json:"edition_id,omitempty"`

	AcquiredAt   *time.Time `json:"acquired_at,omitempty"`
	AcquiredFrom string     `json:"acquired_from"`
	AcquiredBy   *uuid.UUID `json:"acquired_by,omitempty"`

	// Price is minor units plus an ISO 4217 code. A bare number stops being
	// readable the moment a collection spans two currencies, and adding the
	// code afterwards means guessing what the existing rows meant.
	PriceMinor    *int64 `json:"price_minor,omitempty"`
	PriceCurrency string `json:"price_currency"`

	Condition string `json:"condition"`
	IsSigned  bool   `json:"is_signed"`
	Notes     string `json:"notes"`

	LocationID *uuid.UUID `json:"location_id,omitempty"`
	// LocationName is filled by reads that join it, empty on a bare row.
	LocationName string `json:"location_name,omitempty"`

	// OnLoanTo names the borrower when this copy is out, empty otherwise.
	// Filled by reads that join the active loan.
	OnLoanTo string `json:"on_loan_to,omitempty"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"-"`
}

// CopyLocation is a place in a library where copies physically live.
//
// Named for copies rather than plain "location" because storage_locations
// already exists and means filesystem paths for ebook files. Two tables called
// locations meaning different things is a trap.
type CopyLocation struct {
	ID        uuid.UUID  `json:"id"`
	LibraryID uuid.UUID  `json:"library_id"`
	Name      string     `json:"name"`
	ParentID  *uuid.UUID `json:"parent_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	// CopyCount is filled by listing reads.
	CopyCount int `json:"copy_count"`
}
