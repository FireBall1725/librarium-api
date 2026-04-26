// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

type Series struct {
	ID               uuid.UUID  `json:"id"`
	LibraryID        uuid.UUID  `json:"library_id"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	TotalCount       *int       `json:"total_count"`
	Status           string     `json:"status"`
	OriginalLanguage string     `json:"original_language"`
	PublicationYear  *int       `json:"publication_year"`
	Demographic      string     `json:"demographic"`
	Genres           []string   `json:"genres"`
	URL              string     `json:"url"`
	ExternalID       string     `json:"external_id"`
	ExternalSource   string     `json:"external_source"`
	LastReleaseDate  *time.Time `json:"last_release_date"`
	NextReleaseDate  *time.Time `json:"next_release_date"`
	BookCount        int                  `json:"book_count"`
	ArcCount         int                  `json:"arc_count"`
	// Caller-relative reading state. Only populated when the caller is known
	// (List / FindByID receive a non-zero callerID). Both default to 0 when
	// not populated, which is fine because clients gate the indicator behind
	// the user's `show_read_badges` preference anyway.
	ReadCount        int                  `json:"read_count"`
	ReadingCount     int                  `json:"reading_count"`
	PreviewBooks     []SeriesPreviewBook  `json:"preview_books"`
	Tags             []*Tag               `json:"tags"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

// SeriesPreviewBook is the trimmed shape used to assemble a series cover
// mosaic on list views — first 4 books by position, with just enough data
// to render a small thumbnail.
type SeriesPreviewBook struct {
	BookID    uuid.UUID `json:"book_id"`
	Title     string    `json:"title"`
	HasCover  bool      `json:"has_cover"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SeriesArc is an optional named grouping of books inside a series. Manga
// story arcs, multi-trilogy fiction sub-series, etc. A series can have zero
// or more arcs; books opt in by setting their book_series.arc_id.
//
// VolStart / VolEnd are optional bounds the UI uses to place ghost rows
// (missing volumes the user doesn't own) into the right arc even when no
// owned book in the arc gives a neighbour signal.
type SeriesArc struct {
	ID          uuid.UUID `json:"id"`
	SeriesID    uuid.UUID `json:"series_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Position    float64   `json:"position"`
	VolStart    *float64  `json:"vol_start"`
	VolEnd      *float64  `json:"vol_end"`
	BookCount   int       `json:"book_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type SeriesVolume struct {
	ID          uuid.UUID  `json:"id"`
	SeriesID    uuid.UUID  `json:"series_id"`
	Position    float64    `json:"position"`
	Title       string     `json:"title"`
	ReleaseDate *time.Time `json:"release_date"`
	CoverURL    string     `json:"cover_url"`
	ExternalID  string     `json:"external_id"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// SeriesEntry is a book entry within a series, including its reading position.
type SeriesEntry struct {
	Position       float64
	BookID         uuid.UUID
	ArcID          *uuid.UUID
	Title          string
	Subtitle       string
	MediaType      string
	HasCover       bool
	UpdatedAt      time.Time
	UserReadStatus string // empty when caller is anonymous or no interactions
	Contributors   []BookContributor
}

// BookSeriesRef is a lightweight reference used when looking up which series a book belongs to.
type BookSeriesRef struct {
	SeriesID   uuid.UUID `json:"series_id"`
	SeriesName string    `json:"series_name"`
	Position   float64   `json:"position"`
}
