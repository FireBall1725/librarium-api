// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/repository"
)

// defaultPicksMediaTypes is the default set of media types for "Picks of the day"
// when the caller does not specify any. Focuses on longer-form text works.
var defaultPicksMediaTypes = []string{"novel", "light_novel", "non_fiction"}

type DashboardHandler struct {
	books *repository.BookRepo
}

func NewDashboardHandler(books *repository.BookRepo) *DashboardHandler {
	return &DashboardHandler{books: books}
}

// GetCurrentlyReading returns books the caller currently has in progress,
// across all libraries they are a member of.
func (h *DashboardHandler) GetCurrentlyReading(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())

	books, err := h.books.CurrentlyReading(r.Context(), claims.UserID, 24)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	type item struct {
		BookID      string  `json:"book_id"`
		LibraryID   string  `json:"library_id"`
		LibraryName string  `json:"library_name"`
		Title       string  `json:"title"`
		CoverURL    *string `json:"cover_url"`
		Authors     string  `json:"authors"`
		ReadStatus  string  `json:"read_status"`
		UpdatedAt   string  `json:"updated_at"`
	}

	out := make([]item, 0, len(books))
	for _, b := range books {
		it := item{
			BookID:      b.BookID.String(),
			LibraryID:   b.LibraryID.String(),
			LibraryName: b.LibraryName,
			Title:       b.Title,
			Authors:     b.Authors,
			ReadStatus:  "reading",
		}
		if b.UpdatedAt.Valid {
			it.UpdatedAt = b.UpdatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		if b.HasCover {
			u := fmt.Sprintf("/api/v1/libraries/%s/books/%s/cover?v=%d",
				b.LibraryID, b.BookID, b.UpdatedAt.Time.Unix())
			it.CoverURL = &u
		}
		out = append(out, it)
	}

	respond.JSON(w, http.StatusOK, out)
}

// GetRecentlyAdded returns the most recently added books across all the caller's libraries.
func (h *DashboardHandler) GetRecentlyAdded(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())

	books, err := h.books.RecentlyAdded(r.Context(), claims.UserID, 24)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	type item struct {
		BookID      string  `json:"book_id"`
		LibraryID   string  `json:"library_id"`
		LibraryName string  `json:"library_name"`
		Title       string  `json:"title"`
		CoverURL    *string `json:"cover_url"`
		Authors     string  `json:"authors"`
		ReadStatus  string  `json:"read_status"`
	}

	out := make([]item, 0, len(books))
	for _, b := range books {
		it := item{
			BookID:      b.BookID.String(),
			LibraryID:   b.LibraryID.String(),
			LibraryName: b.LibraryName,
			Title:       b.Title,
			Authors:     b.Authors,
			ReadStatus:  b.ReadStatus,
		}
		if b.HasCover {
			u := fmt.Sprintf("/api/v1/libraries/%s/books/%s/cover?v=%d",
				b.LibraryID, b.BookID, b.CreatedAt.Time.Unix())
			it.CoverURL = &u
		}
		out = append(out, it)
	}

	respond.JSON(w, http.StatusOK, out)
}

// GetPicksOfTheDay returns a deterministic pseudo-random sampling of unread
// books from the caller's libraries. The set is stable for the current UTC day
// and rotates at midnight. Pass ?media_type=... (repeatable) to override the
// default selection.
func (h *DashboardHandler) GetPicksOfTheDay(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())

	mediaTypes := r.URL.Query()["media_type"]
	if len(mediaTypes) == 0 {
		mediaTypes = defaultPicksMediaTypes
	}

	seed := time.Now().UTC().Format("2006-01-02")
	books, err := h.books.PicksOfTheDay(r.Context(), claims.UserID, mediaTypes, seed, 24)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	type item struct {
		BookID      string  `json:"book_id"`
		LibraryID   string  `json:"library_id"`
		LibraryName string  `json:"library_name"`
		Title       string  `json:"title"`
		CoverURL    *string `json:"cover_url"`
		Authors     string  `json:"authors"`
		ReadStatus  string  `json:"read_status"`
	}

	out := make([]item, 0, len(books))
	for _, b := range books {
		it := item{
			BookID:      b.BookID.String(),
			LibraryID:   b.LibraryID.String(),
			LibraryName: b.LibraryName,
			Title:       b.Title,
			Authors:     b.Authors,
			ReadStatus:  b.ReadStatus,
		}
		if b.HasCover {
			u := fmt.Sprintf("/api/v1/libraries/%s/books/%s/cover?v=%d",
				b.LibraryID, b.BookID, b.CreatedAt.Time.Unix())
			it.CoverURL = &u
		}
		out = append(out, it)
	}

	respond.JSON(w, http.StatusOK, out)
}

// GetRecentlyFinished returns the most recent books the caller has finished reading.
func (h *DashboardHandler) GetRecentlyFinished(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())

	books, err := h.books.RecentlyFinished(r.Context(), claims.UserID, 12)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	type item struct {
		BookID      string  `json:"book_id"`
		LibraryID   string  `json:"library_id"`
		LibraryName string  `json:"library_name"`
		Title       string  `json:"title"`
		Authors     string  `json:"authors"`
		CoverURL    *string `json:"cover_url"`
		FinishedAt  string  `json:"finished_at"`
		Rating      *int    `json:"rating"`
		IsFavorite  bool    `json:"is_favorite"`
	}

	out := make([]item, 0, len(books))
	for _, b := range books {
		it := item{
			BookID:      b.BookID.String(),
			LibraryID:   b.LibraryID.String(),
			LibraryName: b.LibraryName,
			Title:       b.Title,
			Authors:     b.Authors,
			IsFavorite:  b.IsFavorite,
		}
		if b.FinishedAt.Valid {
			it.FinishedAt = b.FinishedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		if b.Rating.Valid {
			r := int(b.Rating.Int16)
			it.Rating = &r
		}
		if b.HasCover {
			u := fmt.Sprintf("/api/v1/libraries/%s/books/%s/cover?v=%d",
				b.LibraryID, b.BookID, b.FinishedAt.Time.Unix())
			it.CoverURL = &u
		}
		out = append(out, it)
	}

	respond.JSON(w, http.StatusOK, out)
}

// GetContinueSeries returns the next unread book in each series the caller has started.
func (h *DashboardHandler) GetContinueSeries(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())

	books, err := h.books.ContinueSeries(r.Context(), claims.UserID, 12)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	type item struct {
		SeriesID         string  `json:"series_id"`
		SeriesName       string  `json:"series_name"`
		Position         float64 `json:"position"`
		LastReadPosition float64 `json:"last_read_position"`
		BookID           string  `json:"book_id"`
		LibraryID        string  `json:"library_id"`
		LibraryName      string  `json:"library_name"`
		Title            string  `json:"title"`
		Authors          string  `json:"authors"`
		CoverURL         *string `json:"cover_url"`
		ReadStatus       string  `json:"read_status"`
	}

	out := make([]item, 0, len(books))
	for _, b := range books {
		it := item{
			SeriesID:         b.SeriesID.String(),
			SeriesName:       b.SeriesName,
			Position:         b.Position,
			LastReadPosition: b.LastReadPosition,
			BookID:           b.BookID.String(),
			LibraryID:        b.LibraryID.String(),
			LibraryName:      b.LibraryName,
			Title:            b.Title,
			Authors:          b.Authors,
			ReadStatus:       b.ReadStatus,
		}
		if b.HasCover {
			u := fmt.Sprintf("/api/v1/libraries/%s/books/%s/cover?v=%d",
				b.LibraryID, b.BookID, b.UpdatedAt.Time.Unix())
			it.CoverURL = &u
		}
		out = append(out, it)
	}

	respond.JSON(w, http.StatusOK, out)
}

// GetStats returns aggregate reading statistics for the caller.
func (h *DashboardHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())

	stats, err := h.books.GetDashboardStats(r.Context(), claims.UserID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	type monthBucket struct {
		Month string `json:"month"`
		Count int    `json:"count"`
	}
	months := make([]monthBucket, 0, len(stats.MonthlyReads))
	for _, m := range stats.MonthlyReads {
		months = append(months, monthBucket{Month: m.Month, Count: m.Count})
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"total_books":           stats.TotalBooks,
		"books_read":            stats.BooksRead,
		"books_reading":         stats.BooksReading,
		"books_added_this_year": stats.BooksAddedThisYear,
		"books_read_this_year":  stats.BooksReadThisYear,
		"favorites_count":       stats.FavoritesCount,
		"monthly_reads":         months,
	})
}
