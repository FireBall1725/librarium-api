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
// @Param       rating    query string false "Ratings 0-5, comma separated"
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
	if len(sel.Libraries) > 0 {
		readable := make(map[uuid.UUID]bool, len(libraryIDs))
		for _, id := range libraryIDs {
			readable[id] = true
		}
		picked := make([]uuid.UUID, 0, len(sel.Libraries))
		for _, id := range sel.Libraries {
			if readable[id] {
				picked = append(picked, id)
			}
		}
		libraryIDs = picked
	}
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

	facets, err := h.books.Facets(r.Context(), libraryIDs, parseFacetSelection(r), r.URL.Query().Get("q"), callerID)
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

	sel := repository.FacetSelection{
		Ownership:  split("own"),
		ReadStatus: split("status"),
		MediaTypes: split("type"),
		Genres:     split("genre"),
		Tags:       split("tag"),
	}
	for _, s := range split("lib") {
		if id, err := uuid.Parse(s); err == nil {
			sel.Libraries = append(sel.Libraries, id)
		}
	}
	for _, s := range split("rating") {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 && n <= 5 {
			sel.Ratings = append(sel.Ratings, int32(n))
		}
	}
	return sel
}
