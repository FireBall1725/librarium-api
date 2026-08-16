// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"net/http"
	"strings"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

// The cross-library Series and Authors surfaces.
//
// Same shape as /me/books: scope is the caller's own memberships, derived
// server-side, and the surface is the whole collection rather than one library
// you navigated into.
//
// Deliberately not folded into the existing GET /me/series, which is the
// typeahead behind the suggestions modal. That endpoint answers "name a series
// matching these keystrokes" and is capped at twenty; this one answers "show me
// everything I have" and carries counts and cover thumbnails per row. Serving
// both from one route would make every keystroke pay for the index.

// MeBrowseHandler serves the cross-library index surfaces.
type MeBrowseHandler struct {
	libraries    *repository.LibraryRepo
	series       *repository.SeriesRepo
	contributors *repository.ContributorRepo
}

func NewMeBrowseHandler(
	libraries *repository.LibraryRepo,
	series *repository.SeriesRepo,
	contributors *repository.ContributorRepo,
) *MeBrowseHandler {
	return &MeBrowseHandler{libraries: libraries, series: series, contributors: contributors}
}

// seriesIndexVolumes caps the volume strip on a series row.
//
// The strip shows every volume it can, because the gaps are the point: a run
// missing volumes 4 and 9 should look like a run missing volumes 4 and 9. The
// cap only stops a pathological series from turning one row into a thousand-
// element payload, and the client says "+N more" past it.
const seriesIndexVolumes = 60

func callerID(r *http.Request) uuid.UUID {
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		return claims.UserID
	}
	return uuid.Nil
}

// SeriesIndex godoc
//
//	@Summary     Series across every library the caller can read
//	@Description The cross-library Series surface. Unpaged: the A-Z bar has to
//	@Description know which letters have series behind them, which means
//	@Description counting the whole set anyway.
//	@Tags        me
//	@Produce     json
//	@Security    BearerAuth
//	@Param       q  query  string  false  "Name substring"
//	@Success     200  {object}  object{items=[]models.Series,total=int}
//	@Failure     401  {object}  object{error=string}
//	@Router      /me/series/index [get]
func (h *MeBrowseHandler) SeriesIndex(w http.ResponseWriter, r *http.Request) {
	libraryIDs, err := readableLibraryIDs(r, h.libraries)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	items, err := h.series.ListAcross(
		r.Context(), libraryIDs, callerID(r),
		strings.TrimSpace(r.URL.Query().Get("q")), seriesIndexVolumes,
	)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	// Through seriesBody, not the model directly: it is what turns has_cover
	// plus updated_at into the cache-busted cover URL every other series route
	// already returns. Serialising the struct raw would hand this one surface a
	// different shape than the rest of the API.
	bodies := make([]map[string]any, 0, len(items))
	for _, s := range items {
		bodies = append(bodies, seriesBody(s))
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": bodies, "total": len(bodies)})
}

// Counts godoc
//
//	@Summary     Totals for the navigation
//	@Description Book, series and author counts across every library the caller
//	@Description can read. One endpoint rather than three, because the sidebar
//	@Description needs all of them on every page and none of them individually.
//	@Tags        me
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {object}  object{books=int,series=int,authors=int}
//	@Failure     401  {object}  object{error=string}
//	@Router      /me/counts [get]
func (h *MeBrowseHandler) Counts(w http.ResponseWriter, r *http.Request) {
	libraryIDs, err := readableLibraryIDs(r, h.libraries)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	counts, err := h.libraries.CountsForLibraries(r.Context(), libraryIDs)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, counts)
}

// authorBody mirrors previewBooksToBody: the thumbnail URL is assembled here so
// no client has to know how a cover path is spelled.
func authorBody(a *repository.AuthorIndexEntry) map[string]any {
	spines := make([]map[string]any, 0, len(a.Spines))
	for _, s := range a.Spines {
		var coverURL any
		if s.HasCover {
			coverURL = "/api/v1/books/" + s.BookID.String() + "/cover?v=" + itoa(s.UpdatedAt.Unix())
		}
		spines = append(spines, map[string]any{
			"book_id": s.BookID, "title": s.Title, "cover_url": coverURL,
		})
	}
	var photoURL any
	if a.HasPhoto {
		photoURL = "/api/v1/contributors/" + a.ID.String() + "/photo"
	}
	return map[string]any{
		"id":         a.ID,
		"name":       a.Name,
		"sort_name":  a.SortName,
		"letter":     a.Letter,
		"photo_url":  photoURL,
		"book_count": a.BookCount,
		"read_count": a.ReadCount,
		"spines":     spines,
		"libraries":  a.Libraries,
	}
}

// AuthorsIndex godoc
//
//	@Summary     Authors across every library the caller can read
//	@Description The cross-library Authors surface, with per-author book and
//	@Description read counts, cover thumbnails, and the letter each name files
//	@Description under. Unpaged, for the same reason as the series index.
//	@Tags        me
//	@Produce     json
//	@Security    BearerAuth
//	@Param       role  query  string  false  "Contributor roles, comma separated. Defaults to author."
//	@Success     200  {object}  object{items=[]repository.AuthorIndexEntry,total=int}
//	@Failure     401  {object}  object{error=string}
//	@Router      /me/authors/index [get]
func (h *MeBrowseHandler) AuthorsIndex(w http.ResponseWriter, r *http.Request) {
	libraryIDs, err := readableLibraryIDs(r, h.libraries)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	var roles []string
	for _, part := range strings.Split(r.URL.Query().Get("role"), ",") {
		if p := strings.TrimSpace(part); p != "" {
			roles = append(roles, p)
		}
	}

	items, err := h.contributors.ListAuthorIndex(r.Context(), libraryIDs, callerID(r), roles)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	bodies := make([]map[string]any, 0, len(items))
	for _, a := range items {
		bodies = append(bodies, authorBody(a))
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": bodies, "total": len(bodies)})
}
