// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
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
func (h *BookHandler) readableLibraryIDs(r *http.Request) ([]uuid.UUID, error) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return nil, nil
	}
	// A scope-capped token that cannot read books reads nothing, whatever the
	// user's role says.
	if !claims.ScopeAllows("books:read") {
		return []uuid.UUID{}, nil
	}

	access, err := h.libraries.ListAccessForUser(r.Context(), claims.UserID, claims.IsInstanceAdmin)
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
// @Success     200  {object}  object{items=[]object,total=int,page=int,per_page=int}
// @Failure     401  {object}  object{error=string}
// @Router      /me/books [get]
func (h *BookHandler) ListMyBooks(w http.ResponseWriter, r *http.Request) {
	opts, _, err := parseListBooksOpts(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	libraryIDs, err := h.readableLibraryIDs(r)
	if err != nil {
		respond.ServerError(w, r, err)
		return
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
	opts, _, err := parseListBooksOpts(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	libraryIDs, err := h.readableLibraryIDs(r)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	facets, err := h.books.Facets(r.Context(), libraryIDs, opts)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	// The library facet is only meaningful across libraries, so it is built
	// here rather than in the shared facet query: per-library routes would
	// always return a single value with the same count as the total.
	libFacet, err := h.books.LibraryFacet(r.Context(), libraryIDs, opts)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"read_status": facets.ReadStatus,
			"media_type":  facets.MediaType,
			"genre":       facets.Genre,
			"tag":         facets.Tag,
			"rating":      facets.Rating,
			"library":     libFacet,
		},
	})
}
