// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/google/uuid"
)

// EditionFileHandler handles file upload, download, and removal for editions.
type EditionFileHandler struct {
	svc   *service.EditionFileService
	books *service.BookService
}

func NewEditionFileHandler(svc *service.EditionFileService, books *service.BookService) *EditionFileHandler {
	return &EditionFileHandler{svc: svc, books: books}
}

// editionFileBody maps an EditionFile to its JSON representation.
func editionFileBody(ef *models.EditionFile) map[string]any {
	body := map[string]any{
		"id":            ef.ID,
		"edition_id":    ef.EditionID,
		"file_format":   ef.FileFormat,
		"file_name":     ef.FileName,
		"file_path":     ef.FilePath,
		"root_path":     ef.RootPath,
		"display_order": ef.DisplayOrder,
		"created_at":    ef.CreatedAt,
	}
	if ef.StorageLocationID != nil {
		body["storage_location_id"] = ef.StorageLocationID
	} else {
		body["storage_location_id"] = nil
	}
	if ef.FileSize != nil {
		body["file_size"] = *ef.FileSize
	} else {
		body["file_size"] = nil
	}
	return body
}

// ServeEditionFile godoc
//
// @Summary     Download edition file
// @Description Streams a specific file attached to a digital edition.
// @Tags        editions
// @Security    BearerAuth
// @Param       library_id   path  string  true  "Library UUID"
// @Param       book_id      path  string  true  "Book UUID"
// @Param       edition_id   path  string  true  "Edition UUID"
// @Param       file_id      path  string  true  "File UUID"
// @Success     200
// @Failure     404
// @Router      /libraries/{library_id}/books/{book_id}/editions/{edition_id}/files/{file_id} [get]
func (h *EditionFileHandler) ServeEditionFile(w http.ResponseWriter, r *http.Request) {
	editionID, err := uuid.Parse(r.PathValue("edition_id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	fileID, err := uuid.Parse(r.PathValue("file_id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	edition, err := h.books.GetEdition(r.Context(), editionID)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	ef, err := h.svc.FindEditionFile(r.Context(), fileID)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	absPath, mimeType, err := h.svc.GetEditionFilePath(r.Context(), edition, ef)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	downloadName := ef.FileName
	if downloadName == "" {
		downloadName = ef.ID.String() + "." + ef.FileFormat
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+downloadName+`"`)
	http.ServeFile(w, r, absPath)
}

// UploadEditionFile godoc
//
// @Summary     Upload edition file
// @Description Attaches an ebook or audiobook file to an edition.
// @Tags        editions
// @Accept      multipart/form-data
// @Security    BearerAuth
// @Param       library_id   path      string  true  "Library UUID"
// @Param       book_id      path      string  true  "Book UUID"
// @Param       edition_id   path      string  true  "Edition UUID"
// @Param       file         formData  file    true  "Book file (epub, pdf, mp3, m4b, etc.)"
// @Success     201  {object}  object
// @Failure     400  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/books/{book_id}/editions/{edition_id}/files [post]
func (h *EditionFileHandler) UploadEditionFile(w http.ResponseWriter, r *http.Request) {
	editionID, err := uuid.Parse(r.PathValue("edition_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid edition id")
		return
	}
	edition, err := h.books.GetEdition(r.Context(), editionID)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "edition not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	// Max 2 GB upload
	if err := r.ParseMultipartForm(2 << 30); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	ef, err := h.svc.UploadEditionFile(r.Context(), edition, file, header.Filename, header.Size)
	if err != nil {
		var ve *service.ValidationError
		if errors.As(err, &ve) {
			respond.Error(w, http.StatusBadRequest, err.Error())
		} else {
			respond.ServerError(w, r, err)
		}
		return
	}
	respond.JSON(w, http.StatusCreated, editionFileBody(ef))
}

// DeleteEditionFile godoc
//
// @Summary     Remove edition file
// @Description Removes a specific file from an edition.
// @Tags        editions
// @Security    BearerAuth
// @Param       library_id   path  string  true  "Library UUID"
// @Param       book_id      path  string  true  "Book UUID"
// @Param       edition_id   path  string  true  "Edition UUID"
// @Param       file_id      path  string  true  "File UUID"
// @Success     204
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/books/{book_id}/editions/{edition_id}/files/{file_id} [delete]
func (h *EditionFileHandler) DeleteEditionFile(w http.ResponseWriter, r *http.Request) {
	editionID, err := uuid.Parse(r.PathValue("edition_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid edition id")
		return
	}
	fileID, err := uuid.Parse(r.PathValue("file_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid file id")
		return
	}
	edition, err := h.books.GetEdition(r.Context(), editionID)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "edition not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	ef, err := h.svc.FindEditionFile(r.Context(), fileID)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "file not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	if err := h.svc.DeleteEditionFile(r.Context(), edition, ef); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// BrowseUploadPath godoc
//
// @Summary     Browse default upload directory
// @Description Lists files and subdirectories within the server's configured ebook or audiobook upload path.
// @Tags        editions
// @Security    BearerAuth
// @Param       library_id  path   string  true  "Library UUID"
// @Param       format      query  string  true  "ebook or audiobook"
// @Param       path        query  string  false "Relative sub-path (default: root)"
// @Success     200  {object}  object
// @Failure     400  {object}  object{error=string}
// @Router      /libraries/{library_id}/browse-uploads [get]
func (h *EditionFileHandler) BrowseUploadPath(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format != "ebook" && format != "audiobook" && format != "digital" {
		respond.Error(w, http.StatusBadRequest, "format must be ebook or audiobook")
		return
	}
	subPath := r.URL.Query().Get("path")
	rootPath, entries, err := h.svc.BrowseUploadPath(r.Context(), format, subPath)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"root_path": rootPath,
		"entries":   entries,
	})
}

// LinkUploadedFile godoc
//
// @Summary     Link file from upload path to edition
// @Description Links a file that already exists in the server's default upload directory to an edition.
// @Tags        editions
// @Accept      json
// @Security    BearerAuth
// @Param       library_id   path  string  true  "Library UUID"
// @Param       book_id      path  string  true  "Book UUID"
// @Param       edition_id   path  string  true  "Edition UUID"
// @Success     201  {object}  object
// @Failure     400  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/books/{book_id}/editions/{edition_id}/files/link-upload [post]
func (h *EditionFileHandler) LinkUploadedFile(w http.ResponseWriter, r *http.Request) {
	editionID, err := uuid.Parse(r.PathValue("edition_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid edition id")
		return
	}
	edition, err := h.books.GetEdition(r.Context(), editionID)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "edition not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	var body struct {
		FilePath string `json:"file_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.FilePath == "" {
		respond.Error(w, http.StatusBadRequest, "file_path is required")
		return
	}

	ef, err := h.svc.LinkUploadedFile(r.Context(), edition, body.FilePath)
	if err != nil {
		var ve *service.ValidationError
		if errors.As(err, &ve) {
			respond.Error(w, http.StatusBadRequest, err.Error())
		} else {
			respond.ServerError(w, r, err)
		}
		return
	}
	respond.JSON(w, http.StatusCreated, editionFileBody(ef))
}

// BrowseStorageLocation godoc
//
// @Summary     Browse storage location directory
// @Description Lists files and subdirectories within a storage location at the given path.
// @Tags        storage-locations
// @Security    BearerAuth
// @Param       library_id   path   string  true  "Library UUID"
// @Param       location_id  path   string  true  "Storage location UUID"
// @Param       path         query  string  false "Relative sub-path (default: root)"
// @Success     200  {array}   object
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/storage-locations/{location_id}/browse [get]
func (h *EditionFileHandler) BrowseStorageLocation(w http.ResponseWriter, r *http.Request) {
	locationID, err := uuid.Parse(r.PathValue("location_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid location id")
		return
	}
	subPath := r.URL.Query().Get("path")
	entries, err := h.svc.BrowseStorageLocation(r.Context(), locationID, subPath)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "storage location not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, entries)
}

// LinkEditionFile godoc
//
// @Summary     Link existing file to edition
// @Description Links a file that already exists on the server (within a storage location) to an edition.
// @Tags        editions
// @Accept      json
// @Security    BearerAuth
// @Param       library_id   path  string  true  "Library UUID"
// @Param       book_id      path  string  true  "Book UUID"
// @Param       edition_id   path  string  true  "Edition UUID"
// @Success     201  {object}  object
// @Failure     400  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/books/{book_id}/editions/{edition_id}/files/link [post]
func (h *EditionFileHandler) LinkEditionFile(w http.ResponseWriter, r *http.Request) {
	editionID, err := uuid.Parse(r.PathValue("edition_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid edition id")
		return
	}
	edition, err := h.books.GetEdition(r.Context(), editionID)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "edition not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	var body struct {
		StorageLocationID string `json:"storage_location_id"`
		FilePath          string `json:"file_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	locationID, err := uuid.Parse(body.StorageLocationID)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid storage_location_id")
		return
	}
	if body.FilePath == "" {
		respond.Error(w, http.StatusBadRequest, "file_path is required")
		return
	}

	ef, err := h.svc.LinkEditionFile(r.Context(), edition, locationID, body.FilePath)
	if err != nil {
		var ve *service.ValidationError
		if errors.As(err, &ve) {
			respond.Error(w, http.StatusBadRequest, err.Error())
		} else {
			respond.ServerError(w, r, err)
		}
		return
	}
	respond.JSON(w, http.StatusCreated, editionFileBody(ef))
}
