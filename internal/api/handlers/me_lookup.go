// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"net/http"
	"sort"
	"strings"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/google/uuid"
)

// MeLookupHandler powers user-scoped search endpoints used by the Steered
// Suggestions modal. Results are aggregated across every library the caller
// has access to, deduped, and filtered by the `?q=` query term.
type MeLookupHandler struct {
	libSvc     *service.LibraryService
	seriesRepo *repository.SeriesRepo
	tagRepo    *repository.TagRepo
}

func NewMeLookupHandler(libSvc *service.LibraryService, seriesRepo *repository.SeriesRepo, tagRepo *repository.TagRepo) *MeLookupHandler {
	return &MeLookupHandler{libSvc: libSvc, seriesRepo: seriesRepo, tagRepo: tagRepo}
}

// MeSeriesResult is the lightweight shape returned by GET /me/series.
type MeSeriesResult struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	LibraryID   uuid.UUID `json:"library_id"`
	LibraryName string    `json:"library_name"`
}

// MeTagResult is the lightweight shape returned by GET /me/tags. When the
// same tag name exists in multiple accessible libraries, library_name is
// set so the UI can disambiguate.
type MeTagResult struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	LibraryID   uuid.UUID `json:"library_id"`
	LibraryName string    `json:"library_name"`
	// Ambiguous is true when another accessible library has a tag with the
	// same (case-insensitive) name. The client appends " · <library>" in
	// that case.
	Ambiguous bool `json:"ambiguous"`
}

// SearchSeries godoc
//
//	@Summary     Search series across my libraries
//	@Description Returns series from every library the caller can access, filtered by an optional case-insensitive substring in `q`. Results are capped at 20.
//	@Tags        me
//	@Produce     json
//	@Security    BearerAuth
//	@Param       q  query     string  false  "Name substring"
//	@Success     200  {array}   handlers.MeSeriesResult
//	@Failure     401  {object}  object{error=string}
//	@Router      /me/series [get]
func (h *MeLookupHandler) SearchSeries(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	libs, err := h.libSvc.ListLibraries(r.Context(), claims.UserID, claims.IsInstanceAdmin)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	results := make([]MeSeriesResult, 0, 32)
	for _, lib := range libs {
		ss, err := h.seriesRepo.List(r.Context(), lib.ID, q, "")
		if err != nil {
			respond.ServerError(w, r, err)
			return
		}
		for _, s := range ss {
			results = append(results, MeSeriesResult{
				ID:          s.ID,
				Name:        s.Name,
				LibraryID:   lib.ID,
				LibraryName: lib.Name,
			})
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		return strings.ToLower(results[i].Name) < strings.ToLower(results[j].Name)
	})
	if len(results) > 20 {
		results = results[:20]
	}
	respond.JSON(w, http.StatusOK, results)
}

// SearchTags godoc
//
//	@Summary     Search tags across my libraries
//	@Description Returns tags from every library the caller can access, filtered by an optional case-insensitive substring in `q`. Tags are per-library; the response marks names as ambiguous when the same name exists in multiple libraries so the UI can disambiguate. Results are capped at 20.
//	@Tags        me
//	@Produce     json
//	@Security    BearerAuth
//	@Param       q  query     string  false  "Name substring"
//	@Success     200  {array}   handlers.MeTagResult
//	@Failure     401  {object}  object{error=string}
//	@Router      /me/tags [get]
func (h *MeLookupHandler) SearchTags(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	libs, err := h.libSvc.ListLibraries(r.Context(), claims.UserID, claims.IsInstanceAdmin)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	type row struct {
		MeTagResult
		lowerName string
	}
	rows := make([]row, 0, 32)
	nameCounts := map[string]int{}
	for _, lib := range libs {
		ts, err := h.tagRepo.List(r.Context(), lib.ID)
		if err != nil {
			respond.ServerError(w, r, err)
			return
		}
		for _, t := range ts {
			lower := strings.ToLower(t.Name)
			if q != "" && !strings.Contains(lower, q) {
				continue
			}
			rows = append(rows, row{
				MeTagResult: MeTagResult{
					ID:          t.ID,
					Name:        t.Name,
					LibraryID:   lib.ID,
					LibraryName: lib.Name,
				},
				lowerName: lower,
			})
			nameCounts[lower]++
		}
	}

	results := make([]MeTagResult, 0, len(rows))
	for _, r := range rows {
		r.Ambiguous = nameCounts[r.lowerName] > 1
		results = append(results, r.MeTagResult)
	}

	sort.SliceStable(results, func(i, j int) bool {
		li, lj := strings.ToLower(results[i].Name), strings.ToLower(results[j].Name)
		if li != lj {
			return li < lj
		}
		return results[i].LibraryName < results[j].LibraryName
	})
	if len(results) > 20 {
		results = results[:20]
	}
	respond.JSON(w, http.StatusOK, results)
}
