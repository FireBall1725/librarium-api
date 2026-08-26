// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

// Cross-library book browsing.
//
// Every other books route is scoped to one library because the client used to
// navigate into a library first. The redesign makes a library a filter rather
// than a folder, so the default surface is "my books" across every library the
// caller belongs to, with library available as one facet among several.
//
// Scope is always the caller's own memberships. There is no way to ask for a
// library you cannot read: the set is derived server-side from the membership
// table rather than taken from the request, so a crafted library_id cannot
// widen it.

// readableLibraryIDs returns the libraries the caller may read books in.
// Instance admins get every library, matching RequireLibraryPermission, which
// lets them past the per-library membership check.
//
// A free function rather than a method because every cross-library surface
// needs the same answer, and the one thing that must not drift between them is
// what the caller is allowed to see.
func readableLibraryIDs(r *http.Request, libraries *repository.LibraryRepo) ([]uuid.UUID, error) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return nil, nil
	}
	// A scope-capped token that cannot read books reads nothing, whatever the
	// user's role says.
	if !claims.ScopeAllows("books:read") {
		return []uuid.UUID{}, nil
	}

	access, err := libraries.ListAccessForUser(r.Context(), claims.UserID, claims.IsInstanceAdmin)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(access))
	for _, a := range access {
		for _, p := range a.Permissions {
			if p == "books:read" {
				ids = append(ids, a.LibraryID)
				break
			}
		}
	}
	return ids, nil
}

func (h *BookHandler) readableLibraryIDs(r *http.Request) ([]uuid.UUID, error) {
	return readableLibraryIDs(r, h.libraries)
}

// narrowToReadable intersects a requested set of libraries with the readable
// set, so a library filter can only ever shrink the scope.
//
// Intersection rather than substitution is the whole point: the requested ids
// arrive from the client, and any code path that let them replace the readable
// set would turn a query parameter into a way to read someone else's library.
// An empty request means "no filter" and leaves the scope alone.
func narrowToReadable(requested, readable []uuid.UUID) []uuid.UUID {
	if len(requested) == 0 {
		return readable
	}
	allowed := make(map[uuid.UUID]bool, len(readable))
	for _, id := range readable {
		allowed[id] = true
	}
	picked := make([]uuid.UUID, 0, len(requested))
	for _, id := range requested {
		if allowed[id] {
			picked = append(picked, id)
		}
	}
	return picked
}

// libraryIDsFromQuery reads a comma-separated `lib` parameter. Unparseable ids
// are dropped rather than rejected: the parameter is a filter, and a stale
// bookmark holding a deleted library should show nothing, not an error page.
func libraryIDsFromQuery(r *http.Request) []uuid.UUID {
	raw := r.URL.Query().Get("lib")
	if raw == "" {
		return nil
	}
	var ids []uuid.UUID
	for _, part := range strings.Split(raw, ",") {
		if id, err := uuid.Parse(strings.TrimSpace(part)); err == nil {
			ids = append(ids, id)
		}
	}
	// Every id was junk. That is a filter matching nothing, not an absent
	// filter, so it must not fall back to the whole readable set.
	if len(ids) == 0 {
		return []uuid.UUID{uuid.Nil}
	}
	return ids
}

// ListMyBooks godoc
//
// @Summary     Books across every library the caller can read
// @Description The default browse surface. Takes the same filters as the
// @Description per-library list endpoint; library becomes one filter among
// @Description several rather than part of the path.
// @Tags        books
// @Produce     json
// @Param       q         query string false "Search query"
// @Param       page      query int    false "Page number"
// @Param       per_page  query int    false "Results per page"
// @Param       filter    query string false "Structured filter JSON"
// @Param       lib       query string false "Library ids, comma separated"
// @Param       status    query string false "Read statuses, comma separated"
// @Param       type      query string false "Media type names, comma separated"
// @Param       genre     query string false "Genre names, comma separated"
// @Param       tag       query string false "Tag names, comma separated"
// @Param       rating    query string false "Ratings 1-10, comma separated"
// @Param       own       query string false "Ownership states, comma separated: shelf, wishlist, suggested, gap"
// @Param       shelf     query string false "List UUIDs, comma separated"
// @Param       fav       query string false "Favourite: true or false"
// @Param       contributor  query string false "Contributor UUIDs, comma separated"
// @Param       series    query string false "Series UUIDs, comma separated"
// @Param       location  query string false "Location UUIDs, comma separated; matches anything inside them too"
// @Success     200  {object}  object{items=[]object,total=int,page=int,per_page=int}
// @Failure     401  {object}  object{error=string}
// @Router      /me/books [get]
func (h *BookHandler) ListMyBooks(w http.ResponseWriter, r *http.Request) {
	opts, _, err := parseListBooksOpts(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	// The list takes the same facet parameters as the counts beside it. One wire
	// format, one selection struct: a rail that says "Reading 29" and a list
	// that ignores the tick is the failure this prevents.
	sel := parseFacetSelection(r)
	opts.Selection = sel

	libraryIDs, err := h.readableLibraryIDs(r)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	// The library facet narrows the scope rather than adding a WHERE clause,
	// and it narrows by intersection so a library id the caller cannot read
	// cannot widen the result no matter what the client sends.
	libraryIDs = narrowToReadable(sel.Libraries, libraryIDs)
	if len(libraryIDs) == 0 {
		// Not an error. A new user, or one whose only membership was revoked,
		// has an empty shelf and the client renders its own empty state.
		respond.JSON(w, http.StatusOK, map[string]any{
			"items": []any{}, "total": 0, "page": 1, "per_page": opts.PerPage,
		})
		return
	}

	books, total, err := h.books.ListAcross(r.Context(), libraryIDs, opts)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	items := make([]map[string]any, 0, len(books))
	for _, b := range books {
		items = append(items, bookBody(b))
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"items":    items,
		"total":    total,
		"page":     max(opts.Page, 1),
		"per_page": opts.PerPage,
	})
}

// MyBookFacets godoc
//
// @Summary     Facet counts across every library the caller can read
// @Description Counts per facet value over the whole filtered set, so the
// @Description filter rail can be rendered in one request rather than one per
// @Description value. Same filters as /me/books.
// @Tags        books
// @Produce     json
// @Param       q       query string false "Search query"
// @Param       filter  query string false "Structured filter JSON"
// @Success     200  {object}  object{data=repository.BookFacets}
// @Failure     401  {object}  object{error=string}
// @Router      /me/books/facets [get]
func (h *BookHandler) MyBookFacets(w http.ResponseWriter, r *http.Request) {
	libraryIDs, err := h.readableLibraryIDs(r)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	claims := middleware.ClaimsFromContext(r.Context())
	var callerID uuid.UUID
	if claims != nil {
		callerID = claims.UserID
	}

	facets, err := h.books.Facets(r.Context(), libraryIDs, parseFacetSelection(r),
		r.URL.Query().Get("q"), seriesIDsFromQuery(r), callerID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"data": facets})
}

// parseFacetSelection reads the same short parameters the client keeps in its
// URL (lib, status, type, genre, tag, rating), each a comma-separated list.
//
// Deliberately not the structured `filter` JSON the list endpoint takes: the
// facet query needs each dimension separately so it can exclude a dimension's
// own selection when counting it, and flattened filter groups cannot be taken
// apart again.
func parseFacetSelection(r *http.Request) repository.FacetSelection {
	q := r.URL.Query()
	split := func(key string) []string {
		raw := strings.TrimSpace(q.Get(key))
		if raw == "" {
			return nil
		}
		out := make([]string, 0, 4)
		for _, part := range strings.Split(raw, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
		return out
	}

	// The starred flag, as "true" / "false". Anything else is ignored rather
	// than treated as false, so a typo narrows to nothing visible instead of
	// silently answering the opposite question.
	splitBools := func(key string) []bool {
		out := make([]bool, 0, 2)
		for _, v := range split(key) {
			switch strings.ToLower(v) {
			case "true", "1", "yes":
				out = append(out, true)
			case "false", "0", "no":
				out = append(out, false)
			}
		}
		return out
	}

	sel := repository.FacetSelection{
		Ownership:  split("own"),
		ReadStatus: split("status"),
		MediaTypes: split("type"),
		Genres:     split("genre"),
		Tags:       split("tag"),
		Favourites: splitBools("fav"),
	}
	// Shelves come through by id for the same reason libraries do: the name is
	// only unique within one library.
	for _, s := range split("shelf") {
		if id, err := uuid.Parse(s); err == nil {
			sel.Shelves = append(sel.Shelves, id)
		}
	}
	for _, s := range split("lib") {
		if id, err := uuid.Parse(s); err == nil {
			sel.Libraries = append(sel.Libraries, id)
		}
	}
	// By id, because naming an author is a different question from searching
	// for their name: "Tite" the person is not a book with Tite in its title.
	for _, s := range split("contributor") {
		if id, err := uuid.Parse(s); err == nil {
			sel.Contributors = append(sel.Contributors, id)
		}
	}
	// Where the copy sits. By id like a library, because a place name is only
	// unique within the library that holds it.
	for _, s := range split("location") {
		if id, err := uuid.Parse(s); err == nil {
			sel.Locations = append(sel.Locations, id)
		}
	}
	// 1 to 10, which is what user_books stores and what its CHECK enforces.
	// This read 0 to 5, so every rating above 5 was dropped on the way in and
	// the filter silently widened to the whole collection: ticking a rating in
	// the rail returned every book, and the ratings actually in use are 6
	// through 10.
	for _, s := range split("rating") {
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= 10 {
			sel.Ratings = append(sel.Ratings, int32(n))
		}
	}
	return sel
}

// ListMyBooksGrouped godoc
//
// @Summary     Books across every readable library, series collapsed
// @Description Same filters as /me/books, but each series becomes one entry so
// @Description a run and a standalone book are peers. Paging is over entries;
// @Description book_total reports how many books those entries stand for,
// @Description because the facet rail still counts books.
// @Tags        books
// @Produce     json
// @Security    BearerAuth
// @Param       q         query string false "Search query"
// @Param       lib       query string false "Library UUIDs, comma separated"
// @Param       own       query string false "Ownership states, comma separated"
// @Param       status    query string false "Read statuses, comma separated"
// @Param       page      query int    false "Page, 1-based"
// @Param       per_page  query int    false "Entries per page"
// @Success     200  {object}  object{items=[]object,total=int,book_total=int,page=int,per_page=int}
// @Failure     401  {object}  object{error=string}
// @Router      /me/books/grouped [get]
func (h *BookHandler) ListMyBooksGrouped(w http.ResponseWriter, r *http.Request) {
	opts, _, err := parseListBooksOpts(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	opts.Selection = parseFacetSelection(r)

	libraryIDs, err := h.readableLibraryIDs(r)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	libraryIDs = narrowToReadable(opts.Selection.Libraries, libraryIDs)
	if len(libraryIDs) == 0 {
		respond.JSON(w, http.StatusOK, map[string]any{
			"items": []any{}, "total": 0, "book_total": 0,
			"page": 1, "per_page": opts.PerPage,
		})
		return
	}

	entries, total, bookTotal, err := h.books.ListGroupedBySeries(r.Context(), libraryIDs, opts)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	items := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		if e.Series != nil {
			items = append(items, seriesGroupBody(e.Series))
			continue
		}
		if e.Book != nil {
			// The same body the ungrouped list sends, so a standalone entry and
			// a row on /me/books are the same shape and the client renders one.
			items = append(items, map[string]any{"kind": "book", "book": bookBody(e.Book)})
		}
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"items":      items,
		"total":      total,
		"book_total": bookTotal,
		"page":       max(opts.Page, 1),
		"per_page":   opts.PerPage,
	})
}

func seriesGroupBody(g *repository.SeriesGroup) map[string]any {
	var coverURL any
	if g.CoverBookID != nil {
		coverURL = "/api/v1/books/" + g.CoverBookID.String() + "/cover"
	}
	return map[string]any{
		"kind":        "series",
		"series_id":   g.ID,
		"series_name": g.Name,
		"matched":     g.Matched,
		"owned":       g.Owned,
		"read":        g.Read,
		"total_count": g.TotalCount,
		"cover_url":   coverURL,
	}
}

// seriesIDsFromQuery reads a comma-separated `series` parameter.
//
// Several ids rather than one because selecting a page of collapsed groups asks
// for every book in every series on that page, and one request beats one per
// row. Unparseable ids are dropped for the same reason they are in
// libraryIDsFromQuery: a stale link should show nothing, not an error page.
func seriesIDsFromQuery(r *http.Request) []uuid.UUID {
	raw := r.URL.Query().Get("series")
	if raw == "" {
		return nil
	}
	var ids []uuid.UUID
	for _, part := range strings.Split(raw, ",") {
		if id, err := uuid.Parse(strings.TrimSpace(part)); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return []uuid.UUID{uuid.Nil}
	}
	return ids
}
