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
	BookCount        int        `json:"book_count"`
	Tags             []*Tag     `json:"tags"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
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
	Position     float64
	BookID       uuid.UUID
	Title        string
	Subtitle     string
	MediaType    string
	Contributors []BookContributor
}

// BookSeriesRef is a lightweight reference used when looking up which series a book belongs to.
type BookSeriesRef struct {
	SeriesID   uuid.UUID `json:"series_id"`
	SeriesName string    `json:"series_name"`
	Position   float64   `json:"position"`
}
