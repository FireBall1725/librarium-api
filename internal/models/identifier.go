// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import "github.com/google/uuid"

// EditionIdentifier is one way the world names a printing.
//
// ISBN is not privileged here. It is one scheme among several, which matters
// because a 1957 Ace Double has no ISBN and never will: its publisher catalogue
// number is the best identifier in existence for it.
type EditionIdentifier struct {
	EditionID uuid.UUID `json:"edition_id"`
	Scheme    string    `json:"scheme"`
	Value     string    `json:"value"`
}

// IdentifierScheme is a vocabulary row. Adding a scheme is an INSERT rather
// than a migration, which is the whole reason this is a table.
type IdentifierScheme struct {
	Code      string `json:"code"`
	SortOrder int    `json:"sort_order"`
	IsActive  bool   `json:"is_active"`
}

// BookContent links a container work to a work it contains: a manga omnibus
// holding volumes 1 to 3, an anthology, an Ace Double.
//
// It sits at the work level rather than the edition so an anthology's contents
// cannot drift between its printings, and it means "contains exactly", never
// "overlaps".
type BookContent struct {
	ContainerID uuid.UUID `json:"container_id"`
	ContainedID uuid.UUID `json:"contained_id"`
	Position    float64   `json:"position"`
	// Title is the contained work's title, filled by reads that join it. Empty
	// on a bare link.
	Title string `json:"title"`
}

// Vocabulary is one row of a controlled vocabulary: edition formats, copy
// conditions, contributor roles.
//
// Codes only, no display names. A label stored in the database cannot be
// translated, so the name belongs in the locale files and the code is what
// crosses the wire. Adding a format is an INSERT rather than a release, which
// is the whole reason these are tables.
type Vocabulary struct {
	Code      string `json:"code"`
	SortOrder int    `json:"sort_order"`
	IsActive  bool   `json:"is_active"`
	// AppliesTo is work, edition or both, and only contributor roles have it.
	// An illustrator credited on one printing is not a fact about the work.
	AppliesTo string `json:"applies_to,omitempty"`
}
