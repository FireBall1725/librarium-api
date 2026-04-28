// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Edition format constants — the canonical set; no other values are accepted.
const (
	EditionFormatPaperback = "paperback"
	EditionFormatHardcover = "hardcover"
	EditionFormatEbook     = "ebook"
	EditionFormatAudiobook = "audiobook"
	EditionFormatDigital   = "digital"
)

var validEditionFormats = map[string]struct{}{
	EditionFormatPaperback: {},
	EditionFormatHardcover: {},
	EditionFormatEbook:     {},
	EditionFormatAudiobook: {},
	EditionFormatDigital:   {},
}

// NormalizeEditionFormat returns the canonical lowercase format string.
// Unrecognised values fall back to "paperback".
func NormalizeEditionFormat(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if _, ok := validEditionFormats[s]; ok {
		return s
	}
	return EditionFormatPaperback
}

type BookEdition struct {
	ID                     uuid.UUID
	BookID                 uuid.UUID
	Format                 string // paperback | hardcover | ebook | audiobook | digital
	Language               string
	EditionName            string
	Narrator               string
	Publisher              string
	PublishDate            *time.Time
	ISBN10                 string
	ISBN13                 string
	Description            string
	DurationSeconds        *int
	PageCount              *int
	IsPrimary              bool
	CreatedAt              time.Time
	UpdatedAt              time.Time
	NarratorContributorID   *uuid.UUID
	NarratorContributorName string
	// Files is populated by the service layer — not a DB column on book_editions.
	Files []*EditionFile
}

// LibraryBookEdition is a row in the library_book_editions junction — how
// many copies of a given edition a specific library holds, and when that
// library acquired them.
type LibraryBookEdition struct {
	LibraryID     uuid.UUID
	BookEditionID uuid.UUID
	CopyCount     int
	AcquiredAt    *time.Time
	CreatedAt     time.Time
}

type UserBookInteraction struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	BookEditionID uuid.UUID
	ReadStatus    string // unread, reading, read, did_not_finish
	Rating        *int
	Notes         string
	Review        string
	DateStarted   *time.Time
	DateFinished  *time.Time
	IsFavorite    bool
	RereadCount   int
	// Progress is the user's reading progress on this edition. Schema is
	// {pages_read?: int, percent?: float, position?: string} — pages for
	// print, percent for ebook readers, position free text for audio.
	// Empty []byte (or nil) when never set; raw JSON bytes otherwise.
	Progress      []byte
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
