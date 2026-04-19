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

type GenreHandler struct {
	genres *repository.GenreRepo
}

func NewGenreHandler(genres *repository.GenreRepo) *GenreHandler {
	return &GenreHandler{genres: genres}
}

// ListGenres godoc
//
// @Summary     List genres
// @Description Returns all globally defined genres.
// @Tags        genres
// @Produce     json
// @Security    BearerAuth
// @Success     200  {array}   responses.GenreResponse
// @Failure     401  {object}  object{error=string}
// @Router      /genres [get]
func (h *GenreHandler) ListGenres(w http.ResponseWriter, r *http.Request) {
	genres, err := h.genres.List(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, genres)
}

// CreateGenre godoc
//
// @Summary     Create a genre (admin)
// @Description Creates a new global genre. Instance admin only.
// @Tags        genres
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body      object{name=string}  true  "Genre name"
// @Success     201   {object}  responses.GenreResponse
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Failure     403   {object}  object{error=string}
// @Router      /genres [post]
func (h *GenreHandler) CreateGenre(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" {
		respond.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	g, err := h.genres.Create(r.Context(), uuid.New(), body.Name)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, g)
}

// UpdateGenre godoc
//
// @Summary     Update a genre (admin)
// @Description Updates the name of a global genre. Instance admin only.
// @Tags        genres
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       genre_id  path      string  true  "Genre UUID"
// @Param       body      body      object{name=string}  true  "Updated name"
// @Success     200   {object}  responses.GenreResponse
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Failure     403   {object}  object{error=string}
// @Failure     404   {object}  object{error=string}
// @Router      /genres/{genre_id} [put]
func (h *GenreHandler) UpdateGenre(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("genre_id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid genre id")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" {
		respond.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	g, err := h.genres.Update(r.Context(), id, body.Name)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "genre not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, g)
}

// DeleteGenre godoc
//
// @Summary     Delete a genre (admin)
// @Description Permanently deletes a genre. Instance admin only.
// @Tags        genres
// @Security    BearerAuth
// @Param       genre_id  path  string  true  "Genre UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /genres/{genre_id} [delete]
func (h *GenreHandler) DeleteGenre(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("genre_id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid genre id")
		return
	}
	if err := h.genres.Delete(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "genre not found")
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
