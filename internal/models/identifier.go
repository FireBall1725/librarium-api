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
	EditionID uuid.UUID
	Scheme    string
	Value     string
}

// IdentifierScheme is a vocabulary row. Adding a scheme is an INSERT rather
// than a migration, which is the whole reason this is a table.
type IdentifierScheme struct {
	Code      string
	SortOrder int
	IsActive  bool
}

// BookContent links a container work to a work it contains: a manga omnibus
// holding volumes 1 to 3, an anthology, an Ace Double.
//
// It sits at the work level rather than the edition so an anthology's contents
// cannot drift between its printings, and it means "contains exactly", never
// "overlaps".
type BookContent struct {
	ContainerID uuid.UUID
	ContainedID uuid.UUID
	Position    float64
	// Title is the contained work's title, filled by reads that join it. Empty
	// on a bare link.
	Title string
}
