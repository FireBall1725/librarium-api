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
	shelves      *repository.ShelfRepo
	loans        *repository.LoanRepo
}

func NewMeBrowseHandler(
	libraries *repository.LibraryRepo,
	series *repository.SeriesRepo,
	contributors *repository.ContributorRepo,
	shelves *repository.ShelfRepo,
	loans *repository.LoanRepo,
) *MeBrowseHandler {
	return &MeBrowseHandler{
		libraries: libraries, series: series,
		contributors: contributors, shelves: shelves, loans: loans,
	}
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
//	@Param       q        query  string  false  "Name substring"
//	@Param       lib      query  string  false  "Comma-separated library ids to narrow to"
//	@Param       status   query  string  false  "ongoing | completed | hiatus | cancelled"
//	@Param       arcs     query  string  false  "with | without"
//	@Param       reading  query  string  false  "unread | reading | read_all"
//	@Param       tag      query  string  false  "Tag name"
//	@Param       sort     query  string  false  "name | volumes | missing | read | recent"
//	@Param       dir      query  string  false  "asc | desc"
//	@Success     200  {object}  object{items=[]github_com_fireball1725_librarium-api_internal_api_responses.SeriesResponse,total=int}
//	@Failure     401  {object}  object{error=string}
//	@Router      /me/series/index [get]
func (h *MeBrowseHandler) SeriesIndex(w http.ResponseWriter, r *http.Request) {
	libraryIDs, err := readableLibraryIDs(r, h.libraries)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	if len(libraryIDs) == 0 {
		respond.JSON(w, http.StatusOK, map[string]any{
			"items": []any{}, "total": 0, "facets": nil,
		})
		return
	}

	q := r.URL.Query()
	search := strings.TrimSpace(q.Get("q"))
	// Library is a dimension of the rail, not the scope. The per-library Series
	// page redirects here carrying its library, so the parameter has to narrow
	// the rows; but the counts are computed over everything readable, or the
	// rail could only ever offer the library already picked and there would be
	// no way back out of it.
	wanted := libraryIDsFromQuery(r)
	filter := repository.SeriesFilter{
		Libraries: narrowToReadable(wanted, libraryIDs),
		Status:    strings.TrimSpace(q.Get("status")),
		Arcs:      strings.TrimSpace(q.Get("arcs")),
		Reading:   strings.TrimSpace(q.Get("reading")),
		Tag:       strings.TrimSpace(q.Get("tag")),
		Sort:      strings.TrimSpace(q.Get("sort")),
		Desc:      q.Get("dir") == "desc",
	}
	// Asking only for libraries the caller cannot read is not the same as
	// asking for none: an empty narrowing would quietly widen back to
	// everything and show more than was requested.
	if len(wanted) > 0 && len(filter.Libraries) == 0 {
		respond.JSON(w, http.StatusOK, map[string]any{
			"items": []any{}, "total": 0, "facets": nil,
		})
		return
	}

	items, err := h.series.ListAcrossFiltered(
		r.Context(), libraryIDs, callerID(r), search, seriesIndexVolumes, filter,
	)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	// Counts come back with the results in one request. That is what makes the
	// rail worth having: you can see a filter would return nothing before
	// spending a click on it.
	facets, err := h.series.Facets(r.Context(), libraryIDs, callerID(r), search, filter)
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
	respond.JSON(w, http.StatusOK, map[string]any{
		"items": bodies, "total": len(bodies), "facets": facets,
	})
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
//	@Success     200  {object}  object{books=int,series=int,authors=int,loans=int,loans_overdue=int,suggestions=int}
//	@Failure     401  {object}  object{error=string}
//	@Router      /me/counts [get]
func (h *MeBrowseHandler) Counts(w http.ResponseWriter, r *http.Request) {
	libraryIDs, err := readableLibraryIDs(r, h.libraries)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	// Suggestions belong to the caller rather than to a library, so this count
	// needs the user the other five do not.
	var callerID uuid.UUID
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		callerID = claims.UserID
	}

	counts, err := h.libraries.CountsForLibraries(r.Context(), libraryIDs, callerID)
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
//	@Param       lib   query  string  false  "Library UUIDs, comma separated. Narrows the scope; cannot widen it."
//	@Success     200  {object}  object{items=[]repository.AuthorIndexEntry,total=int}
//	@Failure     401  {object}  object{error=string}
//	@Router      /me/authors/index [get]
func (h *MeBrowseHandler) AuthorsIndex(w http.ResponseWriter, r *http.Request) {
	libraryIDs, err := readableLibraryIDs(r, h.libraries)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	// The per-library Contributors page redirects here carrying its library, so
	// this parameter is what keeps that redirect honest. Without it the reader
	// asks for one library's authors and silently gets every library's.
	libraryIDs = narrowToReadable(libraryIDsFromQuery(r), libraryIDs)
	if len(libraryIDs) == 0 {
		respond.JSON(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
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

// MyShelves godoc
//
//	@Summary     Shelves across every library the caller can read
//	@Description The rail lists shelves beside libraries and views, so it needs
//	@Description them across the whole readable scope rather than one library at
//	@Description a time. Each carries its library, colour and icon.
//	@Tags        me
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {object}  object{items=[]github_com_fireball1725_librarium-api_internal_api_responses.ShelfResponse,total=int}
//	@Failure     401  {object}  object{error=string}
//	@Router      /me/shelves [get]
func (h *MeBrowseHandler) MyShelves(w http.ResponseWriter, r *http.Request) {
	libraryIDs, err := readableLibraryIDs(r, h.libraries)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	items, err := h.shelves.ListAcross(r.Context(), libraryIDs)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	// Through shelfBody, not the model. models.Shelf carries no json tags, so
	// serialising it directly emits PascalCase and every field the client reads
	// comes back undefined.
	bodies := make([]map[string]any, 0, len(items))
	for _, sh := range items {
		bodies = append(bodies, shelfBody(sh))
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": bodies, "total": len(bodies)})
}

// MyLoans godoc
//
//	@Summary     Loans across every library the caller can read
//	@Description A loan is a book, a person and some dates; the library is where
//	@Description the book happens to live. "Who has my stuff" is not a question
//	@Description anyone asks one library at a time, so this is the whole set,
//	@Description each row carrying the library it belongs to.
//	@Tags        me
//	@Produce     json
//	@Security    BearerAuth
//	@Param       lib               query  string  false  "Library UUIDs, comma separated. Narrows the scope; cannot widen it."
//	@Param       q                 query  string  false  "Match against borrower or book title"
//	@Param       include_returned  query  bool    false  "Include loans already returned"
//	@Param       overdue           query  bool    false  "Only loans past their due date"
//	@Success     200  {object}  object{items=[]github_com_fireball1725_librarium-api_internal_api_responses.LoanResponse,total=int}
//	@Failure     401  {object}  object{error=string}
//	@Router      /me/loans [get]
func (h *MeBrowseHandler) MyLoans(w http.ResponseWriter, r *http.Request) {
	libraryIDs, err := readableLibraryIDs(r, h.libraries)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	libraryIDs = narrowToReadable(libraryIDsFromQuery(r), libraryIDs)
	if len(libraryIDs) == 0 {
		respond.JSON(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}

	q := r.URL.Query()
	items, err := h.loans.ListAcross(
		r.Context(), libraryIDs,
		q.Get("include_returned") == "true",
		q.Get("overdue") == "true",
		strings.TrimSpace(q.Get("q")),
	)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}
