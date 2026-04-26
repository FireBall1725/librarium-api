// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/google/uuid"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

type SeriesHandler struct {
	svc  *service.SeriesService
	sync *service.ReleaseSyncService
}

func NewSeriesHandler(svc *service.SeriesService, sync *service.ReleaseSyncService) *SeriesHandler {
	return &SeriesHandler{svc: svc, sync: sync}
}

// ListSeries godoc
//
// @Summary     List series in a library
// @Description Returns all series defined in the library.
// @Tags        series
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true   "Library UUID"
// @Param       search      query     string  false  "Filter by series name"
// @Param       tag         query     string  false  "Filter by tag"
// @Success     200  {array}   responses.SeriesResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/series [get]
func (h *SeriesHandler) ListSeries(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	search := r.URL.Query().Get("search")
	tagFilter := r.URL.Query().Get("tag")
	claims := middleware.ClaimsFromContext(r.Context())
	list, err := h.svc.ListSeries(r.Context(), libraryID, claims.UserID, search, tagFilter)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, s := range list {
		out = append(out, seriesBody(s))
	}
	respond.JSON(w, http.StatusOK, out)
}

// CreateSeries godoc
//
// @Summary     Create a series
// @Description Creates a new series in the library.
// @Tags        series
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       body        body      object{name=string,description=string,total_count=integer,status=string,original_language=string,publication_year=integer,demographic=string,genres=[]string,url=string,external_id=string,external_source=string,tag_ids=[]string}  true  "Series details"
// @Success     201  {object}  responses.SeriesResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/series [post]
func (h *SeriesHandler) CreateSeries(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	req, err := decodeSeriesRequest(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	s, err := h.svc.CreateSeries(r.Context(), libraryID, claims.UserID, *req)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, seriesBody(s))
}

// GetSeries godoc
//
// @Summary     Get a series
// @Description Returns details for a specific series.
// @Tags        series
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       series_id   path      string  true  "Series UUID"
// @Success     200  {object}  responses.SeriesResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id} [get]
func (h *SeriesHandler) GetSeries(w http.ResponseWriter, r *http.Request) {
	seriesID, err := uuid.Parse(r.PathValue("series_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid series id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	s, err := h.svc.GetSeries(r.Context(), seriesID, claims.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "series not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, seriesBody(s))
}

// UpdateSeries godoc
//
// @Summary     Update a series
// @Description Replaces a series's metadata.
// @Tags        series
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       series_id   path      string  true  "Series UUID"
// @Param       body        body      object{name=string,description=string,total_count=integer,status=string,original_language=string,publication_year=integer,demographic=string,genres=[]string,url=string,external_id=string,external_source=string,tag_ids=[]string}  true  "Updated series"
// @Success     200  {object}  responses.SeriesResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id} [put]
func (h *SeriesHandler) UpdateSeries(w http.ResponseWriter, r *http.Request) {
	seriesID, err := uuid.Parse(r.PathValue("series_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid series id")
		return
	}
	req, err := decodeSeriesRequest(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	s, err := h.svc.UpdateSeries(r.Context(), seriesID, *req)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "series not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, seriesBody(s))
}

// DeleteSeries godoc
//
// @Summary     Delete a series
// @Description Permanently deletes a series (books are not deleted).
// @Tags        series
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       series_id   path  string  true  "Series UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id} [delete]
func (h *SeriesHandler) DeleteSeries(w http.ResponseWriter, r *http.Request) {
	seriesID, err := uuid.Parse(r.PathValue("series_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid series id")
		return
	}
	if err := h.svc.DeleteSeries(r.Context(), seriesID); errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "series not found")
		return
	} else if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListSeriesBooks godoc
//
// @Summary     List books in a series
// @Description Returns all books in a series with their position.
// @Tags        series
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       series_id   path      string  true  "Series UUID"
// @Success     200  {array}   responses.SeriesEntryResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id}/books [get]
func (h *SeriesHandler) ListSeriesBooks(w http.ResponseWriter, r *http.Request) {
	seriesID, err := uuid.Parse(r.PathValue("series_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid series id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	entries, err := h.svc.ListSeriesBooks(r.Context(), seriesID, claims.UserID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, seriesEntryBody(e))
	}
	respond.JSON(w, http.StatusOK, out)
}

// UpsertSeriesBook godoc
//
// @Summary     Add or update a book in a series
// @Description Sets the position of a book within a series (insert or update). When `arc_id` is present in the body the book is also assigned to that arc; pass an empty string to clear an existing arc assignment without changing it implicitly.
// @Tags        series
// @Accept      json
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       series_id   path  string  true  "Series UUID"
// @Param       body        body  object{book_id=string,position=number,arc_id=string}  true  "Book position and optional arc"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id}/books [post]
func (h *SeriesHandler) UpsertSeriesBook(w http.ResponseWriter, r *http.Request) {
	seriesID, err := uuid.Parse(r.PathValue("series_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid series id")
		return
	}
	// arc_id semantics:
	//   field absent       → leave existing arc assignment untouched
	//   "" (empty string)  → clear the arc assignment
	//   "<uuid>"           → assign to that arc
	var body struct {
		BookID   string  `json:"book_id"`
		Position float64 `json:"position"`
		ArcID    *string `json:"arc_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	bookID, err := uuid.Parse(body.BookID)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid book_id")
		return
	}
	var arcID *uuid.UUID
	if body.ArcID != nil {
		if *body.ArcID == "" {
			zero := uuid.Nil
			arcID = &zero
		} else {
			parsed, err := uuid.Parse(*body.ArcID)
			if err != nil {
				respond.Error(w, http.StatusBadRequest, "invalid arc_id")
				return
			}
			arcID = &parsed
		}
	}
	if err := h.svc.UpsertSeriesBook(r.Context(), seriesID, bookID, body.Position, arcID); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Arcs ──────────────────────────────────────────────────────────────────────

// ListSeriesArcs godoc
//
// @Summary     List arcs for a series
// @Description Returns the named arcs defined for a series, ordered by position.
// @Tags        series
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       series_id   path      string  true  "Series UUID"
// @Success     200  {array}   object{id=string,series_id=string,name=string,description=string,position=number,book_count=integer,created_at=string,updated_at=string}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id}/arcs [get]
func (h *SeriesHandler) ListSeriesArcs(w http.ResponseWriter, r *http.Request) {
	seriesID, err := uuid.Parse(r.PathValue("series_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid series id")
		return
	}
	arcs, err := h.svc.ListSeriesArcs(r.Context(), seriesID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(arcs))
	for _, a := range arcs {
		out = append(out, seriesArcBody(a))
	}
	respond.JSON(w, http.StatusOK, out)
}

// CreateSeriesArc godoc
//
// @Summary     Create an arc within a series
// @Description Adds a new named arc to the series.
// @Tags        series
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       series_id   path      string  true  "Series UUID"
// @Param       body        body      object{name=string,description=string,position=number}  true  "Arc details"
// @Success     201  {object}  object{id=string,series_id=string,name=string,description=string,position=number,book_count=integer,created_at=string,updated_at=string}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id}/arcs [post]
func (h *SeriesHandler) CreateSeriesArc(w http.ResponseWriter, r *http.Request) {
	seriesID, err := uuid.Parse(r.PathValue("series_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid series id")
		return
	}
	req, err := decodeSeriesArcRequest(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	arc, err := h.svc.CreateSeriesArc(r.Context(), seriesID, *req)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, seriesArcBody(arc))
}

// UpdateSeriesArc godoc
//
// @Summary     Update an arc
// @Description Renames or repositions an arc.
// @Tags        series
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       series_id   path      string  true  "Series UUID"
// @Param       arc_id      path      string  true  "Arc UUID"
// @Param       body        body      object{name=string,description=string,position=number}  true  "Arc details"
// @Success     200  {object}  object{id=string,series_id=string,name=string,description=string,position=number,book_count=integer,created_at=string,updated_at=string}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id}/arcs/{arc_id} [put]
func (h *SeriesHandler) UpdateSeriesArc(w http.ResponseWriter, r *http.Request) {
	arcID, err := uuid.Parse(r.PathValue("arc_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid arc id")
		return
	}
	req, err := decodeSeriesArcRequest(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	arc, err := h.svc.UpdateSeriesArc(r.Context(), arcID, *req)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "arc not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, seriesArcBody(arc))
}

// DeleteSeriesArc godoc
//
// @Summary     Delete an arc
// @Description Deletes an arc. Books that were in the arc remain in the series; their arc assignment is cleared.
// @Tags        series
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       series_id   path  string  true  "Series UUID"
// @Param       arc_id      path  string  true  "Arc UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id}/arcs/{arc_id} [delete]
func (h *SeriesHandler) DeleteSeriesArc(w http.ResponseWriter, r *http.Request) {
	arcID, err := uuid.Parse(r.PathValue("arc_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid arc id")
		return
	}
	if err := h.svc.DeleteSeriesArc(r.Context(), arcID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "arc not found")
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetBookSeries godoc
//
// @Summary     Get series for a book
// @Description Returns all series that a specific book belongs to.
// @Tags        series
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       book_id     path      string  true  "Book UUID"
// @Success     200  {array}   object{series_id=string,series_name=string,position=number}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/books/{book_id}/series [get]
func (h *SeriesHandler) GetBookSeries(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	bookID, err := uuid.Parse(r.PathValue("book_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}
	refs, err := h.svc.GetSeriesForBook(r.Context(), libraryID, bookID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		out = append(out, map[string]any{
			"series_id":   ref.SeriesID,
			"series_name": ref.SeriesName,
			"position":    ref.Position,
		})
	}
	respond.JSON(w, http.StatusOK, out)
}

// RemoveSeriesBook godoc
//
// @Summary     Remove a book from a series
// @Description Removes a book's membership from a series.
// @Tags        series
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       series_id   path  string  true  "Series UUID"
// @Param       book_id     path  string  true  "Book UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id}/books/{book_id} [delete]
func (h *SeriesHandler) RemoveSeriesBook(w http.ResponseWriter, r *http.Request) {
	seriesID, err := uuid.Parse(r.PathValue("series_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid series id")
		return
	}
	bookID, err := uuid.Parse(r.PathValue("book_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}
	if err := h.svc.RemoveSeriesBook(r.Context(), seriesID, bookID); errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "book not in series")
		return
	} else if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListSeriesVolumes godoc
//
// @Summary     List series volumes
// @Description Returns known release volumes for a series from provider data.
// @Tags        series
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       series_id   path      string  true  "Series UUID"
// @Success     200  {array}   responses.SeriesVolumeResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id}/volumes [get]
func (h *SeriesHandler) ListSeriesVolumes(w http.ResponseWriter, r *http.Request) {
	seriesID, err := uuid.Parse(r.PathValue("series_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid series id")
		return
	}
	volumes, err := h.svc.ListSeriesVolumes(r.Context(), seriesID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(volumes))
	for _, v := range volumes {
		m := map[string]any{
			"id":          v.ID,
			"series_id":   v.SeriesID,
			"position":    v.Position,
			"title":       v.Title,
			"cover_url":   v.CoverURL,
			"external_id": v.ExternalID,
			"created_at":  v.CreatedAt,
			"updated_at":  v.UpdatedAt,
		}
		if v.ReleaseDate != nil {
			m["release_date"] = v.ReleaseDate.Format("2006-01-02")
		} else {
			m["release_date"] = nil
		}
		out = append(out, m)
	}
	respond.JSON(w, http.StatusOK, out)
}

// SyncSeriesVolumes godoc
//
// @Summary     Sync series volumes from providers
// @Description Fetches the latest volume/release data from providers and stores it.
// @Tags        series
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       series_id   path      string  true  "Series UUID"
// @Success     200  {array}   responses.SeriesVolumeResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     503  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id}/volumes/sync [post]
func (h *SeriesHandler) SyncSeriesVolumes(w http.ResponseWriter, r *http.Request) {
	seriesID, err := uuid.Parse(r.PathValue("series_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid series id")
		return
	}
	if h.sync == nil {
		respond.Error(w, http.StatusServiceUnavailable, "release sync not available")
		return
	}
	if err := h.sync.SyncSeries(r.Context(), seriesID); err != nil {
		slog.Error("sync series volumes failed", "series_id", seriesID, "error", err)
		respond.ServerError(w, r, err)
		return
	}
	volumes, err := h.svc.ListSeriesVolumes(r.Context(), seriesID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(volumes))
	for _, v := range volumes {
		m := map[string]any{
			"id":          v.ID,
			"series_id":   v.SeriesID,
			"position":    v.Position,
			"title":       v.Title,
			"cover_url":   v.CoverURL,
			"external_id": v.ExternalID,
			"created_at":  v.CreatedAt,
			"updated_at":  v.UpdatedAt,
		}
		if v.ReleaseDate != nil {
			m["release_date"] = v.ReleaseDate.Format("2006-01-02")
		} else {
			m["release_date"] = nil
		}
		out = append(out, m)
	}
	respond.JSON(w, http.StatusOK, out)
}

// MatchCandidates godoc
//
// @Summary     Auto-match library books to this series
// @Description Scans the library for books whose title begins with the series
// @Description name plus a volume number, and returns a list of proposed
// @Description (book, position) pairs along with any other series each book
// @Description already belongs to. Does not modify state.
// @Tags        series
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       series_id   path      string  true  "Series UUID"
// @Success     200  {array}   object{book_id=string,title=string,subtitle=string,position=number,other_series=array}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id}/match-candidates [get]
func (h *SeriesHandler) MatchCandidates(w http.ResponseWriter, r *http.Request) {
	seriesID, err := uuid.Parse(r.PathValue("series_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid series id")
		return
	}
	cands, err := h.svc.MatchCandidates(r.Context(), seriesID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(cands))
	for _, c := range cands {
		others := make([]map[string]any, 0, len(c.OtherSeries))
		for _, o := range c.OtherSeries {
			others = append(others, map[string]any{
				"series_id":   o.SeriesID,
				"series_name": o.SeriesName,
				"position":    o.Position,
			})
		}
		out = append(out, map[string]any{
			"book_id":      c.BookID,
			"title":        c.Title,
			"subtitle":     c.Subtitle,
			"position":     c.Position,
			"other_series": others,
		})
	}
	respond.JSON(w, http.StatusOK, out)
}

// ApplyMatches godoc
//
// @Summary     Bulk-apply auto-match results
// @Description Upserts each (book_id, position) pair into the target series.
// @Description Accepts the preview list that the caller has optionally tweaked.
// @Tags        series
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       series_id   path  string  true  "Series UUID"
// @Param       body        body  object{matches=array}  true  "Matches to apply"
// @Success     200  {object}  object{applied=int}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id}/match-apply [post]
func (h *SeriesHandler) ApplyMatches(w http.ResponseWriter, r *http.Request) {
	seriesID, err := uuid.Parse(r.PathValue("series_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid series id")
		return
	}
	var body struct {
		Matches []struct {
			BookID   string  `json:"book_id"`
			Position float64 `json:"position"`
		} `json:"matches"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	applies := make([]service.SeriesMatchApply, 0, len(body.Matches))
	for _, m := range body.Matches {
		bookID, err := uuid.Parse(m.BookID)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid book_id: "+m.BookID)
			return
		}
		applies = append(applies, service.SeriesMatchApply{BookID: bookID, Position: m.Position})
	}
	n, err := h.svc.ApplyMatches(r.Context(), seriesID, applies)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"applied": n})
}

// defaultSuggestMediaTypes is the default media-type filter for the series
// suggester. Focuses on serialized long-form formats that are most likely to
// be part of a numbered series.
var defaultSuggestMediaTypes = []string{"manga", "manhwa", "manhua", "comic", "graphic_novel", "light_novel"}

// SuggestSeries godoc
//
// @Summary     Suggest new series from ungrouped books
// @Description Scans books not yet in any series, detects volume-numbered titles,
// @Description groups them by base name, and returns proposed new series. Does
// @Description not modify state.
// @Tags        series
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path   string  true  "Library UUID"
// @Param       media_type  query  string  false "Media type name filter (repeatable)"
// @Success     200  {array}  object{proposed_name=string,books=array}
// @Failure     400  {object} object{error=string}
// @Failure     401  {object} object{error=string}
// @Router      /libraries/{library_id}/series/suggest [get]
func (h *SeriesHandler) SuggestSeries(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	mediaTypes := r.URL.Query()["media_type"]
	if len(mediaTypes) == 0 {
		mediaTypes = defaultSuggestMediaTypes
	}
	sugs, err := h.svc.SuggestSeries(r.Context(), libraryID, mediaTypes)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(sugs))
	for _, s := range sugs {
		books := make([]map[string]any, 0, len(s.Books))
		for _, b := range s.Books {
			var coverURL *string
			if b.HasCover {
				u := "/api/v1/libraries/" + libraryID.String() + "/books/" + b.BookID.String() + "/cover"
				if b.CreatedAt.Valid {
					u += "?v=" + itoa(b.CreatedAt.Time.Unix())
				}
				coverURL = &u
			}
			books = append(books, map[string]any{
				"book_id":   b.BookID,
				"title":     b.Title,
				"subtitle":  b.Subtitle,
				"position":  b.Position,
				"cover_url": coverURL,
			})
		}
		out = append(out, map[string]any{
			"proposed_name": s.ProposedName,
			"books":         books,
		})
	}
	respond.JSON(w, http.StatusOK, out)
}

// BulkCreateSeries godoc
//
// @Summary     Bulk-create series from suggestions
// @Description Creates each named series and adds the listed books at the given positions.
// @Tags        series
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path   string  true  "Library UUID"
// @Param       body        body   object{series=array}  true  "Series to create"
// @Success     200  {object}  object{created=int,series=array}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/bulk-create [post]
func (h *SeriesHandler) BulkCreateSeries(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	var body struct {
		Series []struct {
			Name  string `json:"name"`
			Books []struct {
				BookID   string  `json:"book_id"`
				Position float64 `json:"position"`
			} `json:"books"`
		} `json:"series"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	items := make([]service.SeriesBulkCreateItem, 0, len(body.Series))
	for _, s := range body.Series {
		books := make([]service.SeriesBulkCreateBook, 0, len(s.Books))
		for _, b := range s.Books {
			bid, err := uuid.Parse(b.BookID)
			if err != nil {
				respond.Error(w, http.StatusBadRequest, "invalid book_id: "+b.BookID)
				return
			}
			books = append(books, service.SeriesBulkCreateBook{BookID: bid, Position: b.Position})
		}
		items = append(items, service.SeriesBulkCreateItem{Name: s.Name, Books: books})
	}
	created, err := h.svc.BulkCreateSeries(r.Context(), libraryID, claims.UserID, items)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(created))
	for _, s := range created {
		out = append(out, seriesBody(s))
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"created": len(created),
		"series":  out,
	})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func decodeSeriesRequest(r *http.Request) (*service.SeriesRequest, error) {
	var body struct {
		Name             string   `json:"name"`
		Description      string   `json:"description"`
		TotalCount       *int     `json:"total_count"`
		IsComplete       bool     `json:"is_complete"`
		Status           string   `json:"status"`
		OriginalLanguage string   `json:"original_language"`
		PublicationYear  *int     `json:"publication_year"`
		Demographic      string   `json:"demographic"`
		Genres           []string `json:"genres"`
		URL              string   `json:"url"`
		ExternalID       string   `json:"external_id"`
		ExternalSource   string   `json:"external_source"`
		TagIDs           []string `json:"tag_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, errors.New("invalid request body")
	}
	if body.Name == "" {
		return nil, errors.New("name is required")
	}
	// Backward compat: if status not set but is_complete sent, derive status
	status := body.Status
	if status == "" {
		if body.IsComplete {
			status = "completed"
		} else {
			status = "ongoing"
		}
	}
	var tagIDs []uuid.UUID
	if body.TagIDs != nil {
		tagIDs = make([]uuid.UUID, 0, len(body.TagIDs))
		for _, s := range body.TagIDs {
			id, err := uuid.Parse(s)
			if err != nil {
				return nil, errors.New("invalid tag_id: " + s)
			}
			tagIDs = append(tagIDs, id)
		}
	}
	return &service.SeriesRequest{
		Name:             body.Name,
		Description:      body.Description,
		TotalCount:       body.TotalCount,
		Status:           status,
		OriginalLanguage: body.OriginalLanguage,
		PublicationYear:  body.PublicationYear,
		Demographic:      body.Demographic,
		Genres:           body.Genres,
		URL:              body.URL,
		ExternalID:       body.ExternalID,
		ExternalSource:   body.ExternalSource,
		TagIDs:           tagIDs,
	}, nil
}

func decodeSeriesArcRequest(r *http.Request) (*service.SeriesArcRequest, error) {
	var body struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Position    float64  `json:"position"`
		VolStart    *float64 `json:"vol_start"`
		VolEnd      *float64 `json:"vol_end"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, errors.New("invalid request body")
	}
	if body.Name == "" {
		return nil, errors.New("name is required")
	}
	return &service.SeriesArcRequest{
		Name:        body.Name,
		Description: body.Description,
		Position:    body.Position,
		VolStart:    body.VolStart,
		VolEnd:      body.VolEnd,
	}, nil
}

func seriesArcBody(a *models.SeriesArc) map[string]any {
	body := map[string]any{
		"id":          a.ID,
		"series_id":   a.SeriesID,
		"name":        a.Name,
		"description": a.Description,
		"position":    a.Position,
		"vol_start":   nil,
		"vol_end":     nil,
		"book_count":  a.BookCount,
		"created_at":  a.CreatedAt,
		"updated_at":  a.UpdatedAt,
	}
	if a.VolStart != nil {
		body["vol_start"] = *a.VolStart
	}
	if a.VolEnd != nil {
		body["vol_end"] = *a.VolEnd
	}
	return body
}

func tagsToBody(tags []*models.Tag) []map[string]any {
	out := make([]map[string]any, 0, len(tags))
	for _, t := range tags {
		out = append(out, map[string]any{"id": t.ID, "name": t.Name, "color": t.Color})
	}
	return out
}

// previewBooksToBody emits the trimmed shape used to render a series cover
// mosaic: book_id, title, and a pre-built cover_url (null when the book has
// no primary cover yet, so the client can render a gradient placeholder).
func previewBooksToBody(preview []models.SeriesPreviewBook) []map[string]any {
	out := make([]map[string]any, 0, len(preview))
	for _, p := range preview {
		var coverURL any
		if p.HasCover {
			coverURL = "/api/v1/books/" + p.BookID.String() + "/cover?v=" + itoa(p.UpdatedAt.Unix())
		}
		out = append(out, map[string]any{
			"book_id":   p.BookID,
			"title":     p.Title,
			"cover_url": coverURL,
		})
	}
	return out
}

func seriesBody(s *models.Series) map[string]any {
	genres := s.Genres
	if genres == nil {
		genres = []string{}
	}
	tags := s.Tags
	if tags == nil {
		tags = []*models.Tag{}
	}
	body := map[string]any{
		"id":                s.ID,
		"library_id":        s.LibraryID,
		"name":              s.Name,
		"description":       s.Description,
		"status":            s.Status,
		"is_complete":       s.Status == "completed", // backward compat
		"original_language": s.OriginalLanguage,
		"demographic":       s.Demographic,
		"genres":            genres,
		"url":               s.URL,
		"external_id":       s.ExternalID,
		"external_source":   s.ExternalSource,
		"book_count":        s.BookCount,
		"arc_count":         s.ArcCount,
		"read_count":        s.ReadCount,
		"reading_count":     s.ReadingCount,
		"preview_books":     previewBooksToBody(s.PreviewBooks),
		"tags":              tagsToBody(tags),
		"created_at":        s.CreatedAt,
		"updated_at":        s.UpdatedAt,
	}
	if s.TotalCount != nil {
		body["total_count"] = *s.TotalCount
	} else {
		body["total_count"] = nil
	}
	if s.PublicationYear != nil {
		body["publication_year"] = *s.PublicationYear
	} else {
		body["publication_year"] = nil
	}
	if s.LastReleaseDate != nil {
		body["last_release_date"] = s.LastReleaseDate.Format("2006-01-02")
	} else {
		body["last_release_date"] = nil
	}
	if s.NextReleaseDate != nil {
		body["next_release_date"] = s.NextReleaseDate.Format("2006-01-02")
	} else {
		body["next_release_date"] = nil
	}
	return body
}

func seriesEntryBody(e *models.SeriesEntry) map[string]any {
	contribs := e.Contributors
	if contribs == nil {
		contribs = []models.BookContributor{}
	}
	var coverURL any
	if e.HasCover {
		coverURL = "/api/v1/books/" + e.BookID.String() + "/cover?v=" + itoa(e.UpdatedAt.Unix())
	}
	body := map[string]any{
		"position":         e.Position,
		"book_id":          e.BookID,
		"title":            e.Title,
		"subtitle":         e.Subtitle,
		"media_type":       e.MediaType,
		"cover_url":        coverURL,
		"user_read_status": e.UserReadStatus,
		"contributors":     contribs,
	}
	if e.ArcID != nil {
		body["arc_id"] = *e.ArcID
	} else {
		body["arc_id"] = nil
	}
	return body
}
