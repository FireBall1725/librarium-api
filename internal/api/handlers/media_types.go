// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

type MediaTypeHandler struct {
	mediaTypes *repository.MediaTypeRepo
}

func NewMediaTypeHandler(mediaTypes *repository.MediaTypeRepo) *MediaTypeHandler {
	return &MediaTypeHandler{mediaTypes: mediaTypes}
}

// ListMediaTypes godoc
//
// @Summary     List media types
// @Description Returns all globally defined media types (book, manga, audiobook, etc.).
// @Tags        media-types
// @Produce     json
// @Security    BearerAuth
// @Success     200  {array}   responses.MediaTypeResponse
// @Failure     401  {object}  object{error=string}
// @Router      /media-types [get]
func (h *MediaTypeHandler) ListMediaTypes(w http.ResponseWriter, r *http.Request) {
	mts, err := h.mediaTypes.List(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, mts)
}

// CreateMediaType godoc
//
// @Summary     Create a media type (admin)
// @Description Creates a new global media type. Instance admin only.
// @Tags        media-types
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body      object{name=string,display_name=string,description=string}  true  "Media type details"
// @Success     201   {object}  responses.MediaTypeResponse
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Failure     403   {object}  object{error=string}
// @Failure     409   {object}  object{error=string}
// @Router      /media-types [post]
func (h *MediaTypeHandler) CreateMediaType(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" {
		respond.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	if body.DisplayName == "" {
		respond.Error(w, http.StatusBadRequest, "display_name is required")
		return
	}
	mt, err := h.mediaTypes.Create(r.Context(), uuid.New(), body.Name, body.DisplayName, body.Description)
	if err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			respond.Error(w, http.StatusConflict, "a media type with that name already exists")
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, mt)
}

// UpdateMediaType godoc
//
// @Summary     Update a media type (admin)
// @Description Updates the display name and description of a media type. Instance admin only.
// @Tags        media-types
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       media_type_id  path      string  true  "Media type UUID"
// @Param       body           body      object{display_name=string,description=string}  true  "Updated fields"
// @Success     200   {object}  responses.MediaTypeResponse
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Failure     403   {object}  object{error=string}
// @Failure     404   {object}  object{error=string}
// @Router      /media-types/{media_type_id} [put]
func (h *MediaTypeHandler) UpdateMediaType(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("media_type_id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid media type id")
		return
	}
	var body struct {
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.DisplayName == "" {
		respond.Error(w, http.StatusBadRequest, "display_name is required")
		return
	}
	mt, err := h.mediaTypes.Update(r.Context(), id, body.DisplayName, body.Description)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "media type not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	// Preserve book_count from in-memory list (not returned by UPDATE)
	respond.JSON(w, http.StatusOK, mt)
}

// DeleteMediaType godoc
//
// @Summary     Delete a media type (admin)
// @Description Permanently deletes a media type. Fails if books are assigned to it. Instance admin only.
// @Tags        media-types
// @Security    BearerAuth
// @Param       media_type_id  path  string  true  "Media type UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Failure     409  {object}  object{error=string}
// @Router      /media-types/{media_type_id} [delete]
func (h *MediaTypeHandler) DeleteMediaType(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("media_type_id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid media type id")
		return
	}
	if err := h.mediaTypes.Delete(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, repository.ErrInUse):
			respond.Error(w, http.StatusConflict, "cannot delete: books are assigned to this media type")
		case errors.Is(err, repository.ErrNotFound):
			respond.Error(w, http.StatusNotFound, "media type not found")
		default:
			respond.ServerError(w, r, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
