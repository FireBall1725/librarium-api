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

// CatalogueHandler serves the world-tier additions: how a printing is
// identified, and what a work contains.
type CatalogueHandler struct {
	identifiers *repository.EditionIdentifierRepo
	contents    *repository.BookContentsRepo
}

func NewCatalogueHandler(identifiers *repository.EditionIdentifierRepo, contents *repository.BookContentsRepo) *CatalogueHandler {
	return &CatalogueHandler{identifiers: identifiers, contents: contents}
}

// ListIdentifierSchemes godoc
//
// @Summary     List the identifier schemes this server knows
// @Description A vocabulary table, so a new scheme is a row rather than a release. Codes only: the display name belongs in the locale files, since a label in the database cannot be translated.
// @Tags        catalogue
// @Produce     json
// @Security    BearerAuth
// @Success     200  {object}  object{items=[]object{code=string,sort_order=int,is_active=bool}}
// @Failure     401  {object}  object{error=string}
// @Router      /identifier-schemes [get]
func (h *CatalogueHandler) ListIdentifierSchemes(w http.ResponseWriter, r *http.Request) {
	schemes, err := h.identifiers.ListSchemes(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": schemes})
}

// ListEditionIdentifiers godoc
//
// @Summary     List a printing's identifiers
// @Description ISBN is one scheme among several. A 1957 Ace Double has no ISBN and never will; its publisher catalogue number is the best identifier in existence for it.
// @Tags        catalogue
// @Produce     json
// @Security    BearerAuth
// @Param       edition_id  path  string  true  "Edition UUID"
// @Success     200  {object}  object{items=[]object{scheme=string,value=string}}
// @Failure     401  {object}  object{error=string}
// @Router      /editions/{edition_id}/identifiers [get]
func (h *CatalogueHandler) ListEditionIdentifiers(w http.ResponseWriter, r *http.Request) {
	editionID, err := uuid.Parse(r.PathValue("edition_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid edition id")
		return
	}
	items, err := h.identifiers.ListForEdition(r.Context(), editionID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items})
}

type identifierBody struct {
	Scheme string `json:"scheme"`
	Value  string `json:"value"`
}

// AddEditionIdentifier godoc
//
// @Summary     Give a printing an identifier
// @Description An edition can carry several, including more than one of a scheme. What cannot happen is two editions claiming the same one, which is the uniqueness the dedup logic always assumed and never had.
// @Tags        catalogue
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       edition_id  path  string  true  "Edition UUID"
// @Param       body  body  object{scheme=string,value=string}  true  "The identifier"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     409  {object}  object{error=string}
// @Router      /editions/{edition_id}/identifiers [post]
func (h *CatalogueHandler) AddEditionIdentifier(w http.ResponseWriter, r *http.Request) {
	editionID, err := uuid.Parse(r.PathValue("edition_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid edition id")
		return
	}

	var body identifierBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err = h.identifiers.Add(r.Context(), editionID, body.Scheme, body.Value)
	switch {
	case errors.Is(err, repository.ErrIdentifierTaken):
		respond.Error(w, http.StatusConflict, "another edition already claims that identifier")
		return
	case errors.Is(err, repository.ErrUnknownScheme):
		respond.Error(w, http.StatusBadRequest, "that identifier scheme is not one this server knows")
		return
	case errors.Is(err, repository.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "edition not found")
		return
	case err != nil:
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RemoveEditionIdentifier godoc
//
// @Summary     Take an identifier off a printing
// @Tags        catalogue
// @Produce     json
// @Security    BearerAuth
// @Param       edition_id  path  string  true  "Edition UUID"
// @Param       scheme      path  string  true  "Scheme code, e.g. isbn13"
// @Param       value       path  string  true  "The identifier value"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Router      /editions/{edition_id}/identifiers/{scheme}/{value} [delete]
func (h *CatalogueHandler) RemoveEditionIdentifier(w http.ResponseWriter, r *http.Request) {
	editionID, err := uuid.Parse(r.PathValue("edition_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid edition id")
		return
	}
	if err := h.identifiers.Remove(r.Context(), editionID,
		r.PathValue("scheme"), r.PathValue("value")); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListBookContents godoc
//
// @Summary     List what a work contains
// @Description An omnibus holding volumes 1 to 3, an anthology, an Ace Double. Ownership resolves through this, so holding the container counts as holding what is inside it.
// @Tags        catalogue
// @Produce     json
// @Security    BearerAuth
// @Param       book_id  path  string  true  "Book UUID"
// @Success     200  {object}  object{items=[]object{contained_id=string,title=string,position=number}}
// @Failure     401  {object}  object{error=string}
// @Router      /books/{book_id}/contents [get]
func (h *CatalogueHandler) ListBookContents(w http.ResponseWriter, r *http.Request) {
	bookID, ok := bookIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}
	items, err := h.contents.ListContents(r.Context(), bookID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items})
}

// ListBookContainers godoc
//
// @Summary     List the works that contain this one
// @Description The reverse direction, which answers "I own the 3-in-1, so do I have volume 2".
// @Tags        catalogue
// @Produce     json
// @Security    BearerAuth
// @Param       book_id  path  string  true  "Book UUID"
// @Success     200  {object}  object{items=[]object{container_id=string,title=string}}
// @Failure     401  {object}  object{error=string}
// @Router      /books/{book_id}/containers [get]
func (h *CatalogueHandler) ListBookContainers(w http.ResponseWriter, r *http.Request) {
	bookID, ok := bookIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}
	items, err := h.contents.ListContainers(r.Context(), bookID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items})
}

type contentBody struct {
	ContainedID string  `json:"contained_id"`
	Position    float64 `json:"position"`
}

// AddBookContent godoc
//
// @Summary     Record that a work contains another
// @Description Containment means contains exactly, never overlaps. A re-cut edition whose volume boundaries do not line up is modelled as its own series instead.
// @Tags        catalogue
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       book_id  path  string  true  "Container book UUID"
// @Param       body  body  object{contained_id=string,position=number}  true  "What it contains"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     409  {object}  object{error=string}
// @Router      /books/{book_id}/contents [post]
func (h *CatalogueHandler) AddBookContent(w http.ResponseWriter, r *http.Request) {
	containerID, ok := bookIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}

	var body contentBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	containedID, err := uuid.Parse(body.ContainedID)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid contained book id")
		return
	}

	err = h.contents.Add(r.Context(), containerID, containedID, body.Position)
	switch {
	case errors.Is(err, repository.ErrContainmentCycle):
		// 409 rather than 400: the request is well formed, the catalogue is
		// just already shaped in a way that makes it impossible.
		respond.Error(w, http.StatusConflict,
			"that would make a work contain itself, directly or through what it already holds")
		return
	case errors.Is(err, repository.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "book not found")
		return
	case err != nil:
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RemoveBookContent godoc
//
// @Summary     Remove a containment link
// @Tags        catalogue
// @Produce     json
// @Security    BearerAuth
// @Param       book_id       path  string  true  "Container book UUID"
// @Param       contained_id  path  string  true  "Contained book UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Router      /books/{book_id}/contents/{contained_id} [delete]
func (h *CatalogueHandler) RemoveBookContent(w http.ResponseWriter, r *http.Request) {
	containerID, ok := bookIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}
	containedID, err := uuid.Parse(r.PathValue("contained_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid contained book id")
		return
	}
	if err := h.contents.Remove(r.Context(), containerID, containedID); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
