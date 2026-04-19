// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/providers"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/google/uuid"
)

type ContributorHandler struct {
	svc *service.ContributorService
}

func NewContributorHandler(svc *service.ContributorService) *ContributorHandler {
	return &ContributorHandler{svc: svc}
}

// ListForLibrary godoc
//
// @Summary     List contributors for a library (paged)
// @Description Returns a paginated, filtered, sorted list of contributors.
// @Tags        contributors
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path    string  true   "Library UUID"
// @Param       q           query   string  false  "Name filter"
// @Param       letter      query   string  false  "First-letter filter"
// @Param       sort        query   string  false  "Sort field: name|book_count"
// @Param       sort_dir    query   string  false  "Sort direction: asc|desc"
// @Param       page        query   integer false  "Page number"
// @Param       per_page    query   integer false  "Items per page"
// @Success     200  {object}  object
// @Router      /libraries/{library_id}/contributors [get]
func (h *ContributorHandler) ListForLibrary(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}

	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("per_page"))
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 25
	}
	if perPage > 200 {
		perPage = 200
	}
	sort := q.Get("sort")
	if sort == "" {
		sort = "name"
	}
	sortDir := q.Get("sort_dir")
	if !strings.EqualFold(sortDir, "desc") {
		sortDir = "asc"
	}

	opts := repository.ContributorListOpts{
		Search:  q.Get("q"),
		Letter:  strings.ToUpper(q.Get("letter")),
		Sort:    sort,
		SortDir: sortDir,
		Page:    page,
		PerPage: perPage,
	}

	contributors, total, err := h.svc.ListForLibraryPaged(r.Context(), libraryID, opts)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	out := make([]map[string]any, 0, len(contributors))
	for _, c := range contributors {
		out = append(out, contributorListBody(c))
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"items":    out,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

// GetContributorLetters godoc
//
// @Summary     Get available first letters for contributors in a library
// @Tags        contributors
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Success     200  {array}  string
// @Router      /libraries/{library_id}/contributors/letters [get]
func (h *ContributorHandler) GetLetters(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	letters, err := h.svc.LettersForLibrary(r.Context(), libraryID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	if letters == nil {
		letters = []string{}
	}
	respond.JSON(w, http.StatusOK, letters)
}

// DeleteContributor godoc
//
// @Summary     Delete a contributor
// @Description Hard-deletes a contributor. Returns 409 if they still have books.
// @Tags        contributors
// @Security    BearerAuth
// @Param       contributor_id  path  string  true  "Contributor UUID"
// @Success     204
// @Failure     409  {object}  object{error=string}
// @Router      /contributors/{contributor_id} [delete]
func (h *ContributorHandler) DeleteContributor(w http.ResponseWriter, r *http.Request) {
	contributorID, err := uuid.Parse(r.PathValue("contributor_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid contributor id")
		return
	}
	if err := h.svc.DeleteContributor(r.Context(), contributorID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, repository.ErrInUse) {
			respond.Error(w, http.StatusConflict, "contributor still has books")
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UpdateContributor godoc
//
// @Summary     Update a contributor's profile fields
// @Tags        contributors
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       contributor_id  path  string  true  "Contributor UUID"
// @Param       body            body  object  true  "Payload (all fields optional)"
// @Success     200  {object}  object
// @Failure     404  {object}  object{error=string}
// @Router      /contributors/{contributor_id} [patch]
func (h *ContributorHandler) UpdateContributor(w http.ResponseWriter, r *http.Request) {
	contributorID, err := uuid.Parse(r.PathValue("contributor_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid contributor id")
		return
	}
	var body struct {
		Name        string  `json:"name"`
		SortName    *string `json:"sort_name"`
		IsCorporate *bool   `json:"is_corporate"`
		Bio         *string `json:"bio"`
		BornDate    *string `json:"born_date"`
		DiedDate    *string `json:"died_date"`
		Nationality *string `json:"nationality"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	c, err := h.svc.UpdateContributor(r.Context(), contributorID, service.UpdateContributorInput{
		Name:        body.Name,
		SortName:    body.SortName,
		IsCorporate: body.IsCorporate,
		Bio:         body.Bio,
		BornDate:    body.BornDate,
		DiedDate:    body.DiedDate,
		Nationality: body.Nationality,
	})
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, contributorDetailBody(c))
}

// GetContributor godoc
//
// @Summary     Get a contributor with works
// @Description Returns full contributor profile and bibliography, annotated with library context.
// @Tags        contributors
// @Produce     json
// @Security    BearerAuth
// @Param       library_id       path  string  true  "Library UUID"
// @Param       contributor_id   path  string  true  "Contributor UUID"
// @Success     200  {object}  object
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/contributors/{contributor_id} [get]
func (h *ContributorHandler) GetContributor(w http.ResponseWriter, r *http.Request) {
	contributorID, err := uuid.Parse(r.PathValue("contributor_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid contributor id")
		return
	}
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}

	claims := middleware.ClaimsFromContext(r.Context())
	c, works, libraryBooks, err := h.svc.GetContributor(r.Context(), contributorID, libraryID, claims.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	worksOut := make([]map[string]any, 0, len(works))
	for _, wk := range works {
		worksOut = append(worksOut, workBody(wk))
	}

	booksOut := make([]map[string]any, 0, len(libraryBooks))
	for _, b := range libraryBooks {
		booksOut = append(booksOut, bookBody(b))
	}

	body := contributorDetailBody(c)
	body["works"] = worksOut
	body["books"] = booksOut
	respond.JSON(w, http.StatusOK, body)
}

// UploadContributorPhoto godoc
//
// @Summary     Upload contributor photo
// @Description Accepts a multipart form upload and stores it as the contributor's primary photo.
// @Tags        contributors
// @Accept      multipart/form-data
// @Security    BearerAuth
// @Param       contributor_id  path      string  true  "Contributor UUID"
// @Param       photo           formData  file    true  "Photo file"
// @Success     204
// @Router      /contributors/{contributor_id}/photo [put]
func (h *ContributorHandler) UploadContributorPhoto(w http.ResponseWriter, r *http.Request) {
	contributorID, err := uuid.Parse(r.PathValue("contributor_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid contributor id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("photo")
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "photo file is required")
		return
	}
	defer file.Close()

	mime := header.Header.Get("Content-Type")
	if mime == "" || !strings.HasPrefix(mime, "image/") {
		respond.Error(w, http.StatusBadRequest, "file must be an image")
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, 10<<20))
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	if err := h.svc.StorePhotoFromUpload(r.Context(), contributorID, claims.UserID, data, mime); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteContributorPhotoHandler godoc
//
// @Summary     Delete contributor photo
// @Description Removes the primary photo for a contributor.
// @Tags        contributors
// @Security    BearerAuth
// @Param       contributor_id  path  string  true  "Contributor UUID"
// @Success     204
// @Router      /contributors/{contributor_id}/photo [delete]
func (h *ContributorHandler) DeleteContributorPhotoHandler(w http.ResponseWriter, r *http.Request) {
	contributorID, err := uuid.Parse(r.PathValue("contributor_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid contributor id")
		return
	}
	if err := h.svc.DeleteContributorPhoto(r.Context(), contributorID); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ServeContributorPhoto godoc
//
// @Summary     Serve contributor photo
// @Description Streams the primary photo for a contributor.
// @Tags        contributors
// @Produce     image/jpeg
// @Security    BearerAuth
// @Param       contributor_id  path  string  true  "Contributor UUID"
// @Success     200
// @Failure     404
// @Router      /contributors/{contributor_id}/photo [get]
func (h *ContributorHandler) ServeContributorPhoto(w http.ResponseWriter, r *http.Request) {
	contributorID, err := uuid.Parse(r.PathValue("contributor_id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	filePath, mimeType, err := h.svc.GetContributorPhotoPath(r.Context(), contributorID)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Type", mimeType)
	http.ServeFile(w, r, filePath)
}

// SearchExternalContributors godoc
//
// @Summary     Search external providers for contributors
// @Description Queries enabled ContributorProviders for name candidates.
// @Tags        contributors
// @Produce     json
// @Security    BearerAuth
// @Param       q  query  string  true  "Search query"
// @Success     200  {array}  object
// @Router      /lookup/contributors [get]
func (h *ContributorHandler) SearchExternalContributors(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < 2 {
		respond.JSON(w, http.StatusOK, []any{})
		return
	}

	candidates, err := h.svc.SearchExternal(r.Context(), q)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	out := make([]map[string]any, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, map[string]any{
			"provider":    c.Provider,
			"external_id": c.ExternalID,
			"name":        c.Name,
			"photo_url":   c.PhotoURL,
		})
	}
	respond.JSON(w, http.StatusOK, out)
}

// FetchContributorMetadata godoc
//
// @Summary     Fetch contributor metadata from a provider
// @Description Fetches full profile + bibliography from a specific provider without persisting.
// @Tags        contributors
// @Produce     json
// @Security    BearerAuth
// @Param       contributor_id  path   string  true  "Contributor UUID"
// @Param       provider        query  string  true  "Provider name"
// @Param       external_id     query  string  true  "Provider-specific ID"
// @Success     200  {object}  object
// @Failure     404  {object}  object{error=string}
// @Router      /contributors/{contributor_id}/metadata/fetch [get]
func (h *ContributorHandler) FetchContributorMetadata(w http.ResponseWriter, r *http.Request) {
	providerName := r.URL.Query().Get("provider")
	externalID := r.URL.Query().Get("external_id")
	if providerName == "" || externalID == "" {
		respond.Error(w, http.StatusBadRequest, "provider and external_id are required")
		return
	}

	data, err := h.svc.FetchFromProvider(r.Context(), providerName, externalID)
	if err != nil {
		slog.ErrorContext(r.Context(), "contributor metadata fetch failed",
			"provider", providerName,
			"external_id", externalID,
			"error", err,
		)
		respond.Error(w, http.StatusBadGateway, fmt.Sprintf("provider error: %s", err.Error()))
		return
	}
	if data == nil {
		http.NotFound(w, r)
		return
	}

	worksOut := make([]map[string]any, 0, len(data.Works))
	for _, wk := range data.Works {
		worksOut = append(worksOut, map[string]any{
			"title":        wk.Title,
			"isbn_13":      wk.ISBN13,
			"isbn_10":      wk.ISBN10,
			"publish_year": wk.PublishYear,
			"cover_url":    wk.CoverURL,
		})
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"provider":    data.Provider,
		"external_id": data.ExternalID,
		"name":        data.Name,
		"bio":         data.Bio,
		"born_date":   data.BornDate,
		"died_date":   data.DiedDate,
		"nationality": data.Nationality,
		"photo_url":   data.PhotoURL,
		"works":       worksOut,
	})
}

// ApplyContributorMetadata godoc
//
// @Summary     Apply metadata to a contributor
// @Description Writes provider-sourced fields and bibliography to the contributor record.
// @Tags        contributors
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       contributor_id  path  string  true  "Contributor UUID"
// @Param       body            body  object  true  "Metadata payload"
// @Success     200  {object}  object
// @Failure     400  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /contributors/{contributor_id}/metadata/apply [post]
func (h *ContributorHandler) ApplyContributorMetadata(w http.ResponseWriter, r *http.Request) {
	contributorID, err := uuid.Parse(r.PathValue("contributor_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid contributor id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())

	var body struct {
		Provider    string     `json:"provider"`
		ExternalID  string     `json:"external_id"`
		Bio         string     `json:"bio"`
		BornDate    *time.Time `json:"born_date"`
		DiedDate    *time.Time `json:"died_date"`
		Nationality string     `json:"nationality"`
		PhotoURL    string     `json:"photo_url"`
		Works       []struct {
			Title       string `json:"title"`
			ISBN13      string `json:"isbn_13"`
			ISBN10      string `json:"isbn_10"`
			PublishYear *int   `json:"publish_year"`
			CoverURL    string `json:"cover_url"`
		} `json:"works"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	data := &providers.ContributorData{
		Provider:    body.Provider,
		ExternalID:  body.ExternalID,
		Bio:         body.Bio,
		BornDate:    body.BornDate,
		DiedDate:    body.DiedDate,
		Nationality: body.Nationality,
		PhotoURL:    body.PhotoURL,
	}
	for _, wk := range body.Works {
		data.Works = append(data.Works, providers.ContributorWorkResult{
			Title:       wk.Title,
			ISBN13:      wk.ISBN13,
			ISBN10:      wk.ISBN10,
			PublishYear: wk.PublishYear,
			CoverURL:    wk.CoverURL,
		})
	}

	c, err := h.svc.ApplyMetadata(r.Context(), contributorID, claims.UserID, data)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	respond.JSON(w, http.StatusOK, contributorDetailBody(c))
}

// DeleteContributorWork godoc
//
// @Summary     Delete a contributor work
// @Description Soft-deletes a bibliography entry for a contributor.
// @Tags        contributors
// @Security    BearerAuth
// @Param       contributor_id  path  string  true  "Contributor UUID"
// @Param       work_id         path  string  true  "Work UUID"
// @Success     204
// @Failure     404  {object}  object{error=string}
// @Router      /contributors/{contributor_id}/works/{work_id} [delete]
func (h *ContributorHandler) DeleteContributorWork(w http.ResponseWriter, r *http.Request) {
	workID, err := uuid.Parse(r.PathValue("work_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid work id")
		return
	}

	if err := h.svc.DeleteWork(r.Context(), workID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Response helpers ─────────────────────────────────────────────────────────

func contributorPhotoURL(c *models.Contributor) any {
	if !c.HasPhoto {
		return nil
	}
	return fmt.Sprintf("/api/v1/contributors/%s/photo?v=%d", c.ID, c.UpdatedAt.Unix())
}

func contributorListBody(c *models.Contributor) map[string]any {
	return map[string]any{
		"id":           c.ID,
		"name":         c.Name,
		"sort_name":    c.SortName,
		"is_corporate": c.IsCorporate,
		"photo_url":    contributorPhotoURL(c),
		"book_count":   c.BookCount,
		"nationality":  c.Nationality,
		"born_date":    c.BornDate,
		"updated_at":   c.UpdatedAt,
	}
}

func contributorDetailBody(c *models.Contributor) map[string]any {
	return map[string]any{
		"id":           c.ID,
		"name":         c.Name,
		"sort_name":    c.SortName,
		"is_corporate": c.IsCorporate,
		"bio":          c.Bio,
		"born_date":    c.BornDate,
		"died_date":    c.DiedDate,
		"nationality":  c.Nationality,
		"external_ids": c.ExternalIDs,
		"photo_url":    contributorPhotoURL(c),
		"book_count":   c.BookCount,
		"created_at":   c.CreatedAt,
		"updated_at":   c.UpdatedAt,
	}
}

func workBody(wk *models.ContributorWork) map[string]any {
	return map[string]any{
		"id":              wk.ID,
		"contributor_id":  wk.ContributorID,
		"title":           wk.Title,
		"isbn_13":         wk.ISBN13,
		"isbn_10":         wk.ISBN10,
		"publish_year":    wk.PublishYear,
		"cover_url":       wk.CoverURL,
		"source":          wk.Source,
		"created_at":      wk.CreatedAt,
		"in_library":      wk.InLibrary,
		"library_book_id": wk.LibraryBookID,
	}
}
