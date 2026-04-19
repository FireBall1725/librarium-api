// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/google/uuid"
)

type ShelfHandler struct {
	svc *service.ShelfService
}

func NewShelfHandler(svc *service.ShelfService) *ShelfHandler {
	return &ShelfHandler{svc: svc}
}

// ─── Shelves ──────────────────────────────────────────────────────────────────

// ListShelves godoc
//
// @Summary     List shelves in a library
// @Description Returns all shelves for a library with book counts and tags.
// @Tags        shelves
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true   "Library UUID"
// @Param       search      query     string  false  "Filter by shelf name"
// @Param       tag         query     string  false  "Filter by tag name"
// @Success     200  {array}   responses.ShelfResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/shelves [get]
func (h *ShelfHandler) ListShelves(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	search := r.URL.Query().Get("search")
	tagFilter := r.URL.Query().Get("tag")
	shelves, err := h.svc.ListShelves(r.Context(), libraryID, search, tagFilter)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(shelves))
	for _, s := range shelves {
		out = append(out, shelfBody(s))
	}
	respond.JSON(w, http.StatusOK, out)
}

// CreateShelf godoc
//
// @Summary     Create a shelf
// @Description Creates a new shelf in the library.
// @Tags        shelves
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       body        body      object{name=string,description=string,color=string,icon=string,display_order=integer,tag_ids=[]string}  true  "Shelf details"
// @Success     201  {object}  responses.ShelfResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/shelves [post]
func (h *ShelfHandler) CreateShelf(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	req, err := decodeShelfRequest(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	shelf, err := h.svc.CreateShelf(r.Context(), libraryID, claims.UserID,
		req.Name, req.Description, req.Color, req.Icon, req.DisplayOrder, req.TagIDs)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, shelfBody(shelf))
}

// UpdateShelf godoc
//
// @Summary     Update a shelf
// @Description Replaces a shelf's metadata.
// @Tags        shelves
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       shelf_id    path      string  true  "Shelf UUID"
// @Param       body        body      object{name=string,description=string,color=string,icon=string,display_order=integer,tag_ids=[]string}  true  "Updated shelf"
// @Success     200  {object}  responses.ShelfResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/shelves/{shelf_id} [put]
func (h *ShelfHandler) UpdateShelf(w http.ResponseWriter, r *http.Request) {
	shelfID, err := uuid.Parse(r.PathValue("shelf_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid shelf id")
		return
	}
	req, err := decodeShelfRequest(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	shelf, err := h.svc.UpdateShelf(r.Context(), shelfID,
		req.Name, req.Description, req.Color, req.Icon, req.DisplayOrder, req.TagIDs)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "shelf not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, shelfBody(shelf))
}

// DeleteShelf godoc
//
// @Summary     Delete a shelf
// @Description Permanently deletes a shelf (books are not deleted).
// @Tags        shelves
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       shelf_id    path  string  true  "Shelf UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/shelves/{shelf_id} [delete]
func (h *ShelfHandler) DeleteShelf(w http.ResponseWriter, r *http.Request) {
	shelfID, err := uuid.Parse(r.PathValue("shelf_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid shelf id")
		return
	}
	if err := h.svc.DeleteShelf(r.Context(), shelfID); errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "shelf not found")
		return
	} else if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListBookShelves godoc
//
// @Summary     List shelves for a book
// @Description Returns all shelves that contain the given book.
// @Tags        shelves
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       book_id     path      string  true  "Book UUID"
// @Success     200  {array}   responses.ShelfResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/books/{book_id}/shelves [get]
func (h *ShelfHandler) ListBookShelves(w http.ResponseWriter, r *http.Request) {
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
	shelves, err := h.svc.ListBookShelves(r.Context(), libraryID, bookID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(shelves))
	for _, s := range shelves {
		out = append(out, shelfBody(s))
	}
	respond.JSON(w, http.StatusOK, out)
}

// ListShelfBooks godoc
//
// @Summary     List books on a shelf
// @Description Returns all books currently on the given shelf.
// @Tags        shelves
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       shelf_id    path      string  true  "Shelf UUID"
// @Success     200  {array}   responses.BookResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/shelves/{shelf_id}/books [get]
func (h *ShelfHandler) ListShelfBooks(w http.ResponseWriter, r *http.Request) {
	shelfID, err := uuid.Parse(r.PathValue("shelf_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid shelf id")
		return
	}
	books, err := h.svc.ListShelfBooks(r.Context(), shelfID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(books))
	for _, b := range books {
		out = append(out, bookBody(b))
	}
	respond.JSON(w, http.StatusOK, out)
}

// AddBookToShelf godoc
//
// @Summary     Add a book to a shelf
// @Description Adds a book to the specified shelf.
// @Tags        shelves
// @Accept      json
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       shelf_id    path  string  true  "Shelf UUID"
// @Param       body        body  object{book_id=string}  true  "Book to add"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/shelves/{shelf_id}/books [post]
func (h *ShelfHandler) AddBookToShelf(w http.ResponseWriter, r *http.Request) {
	shelfID, err := uuid.Parse(r.PathValue("shelf_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid shelf id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())

	var body struct {
		BookID string `json:"book_id"`
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
	if err := h.svc.AddBookToShelf(r.Context(), shelfID, bookID, claims.UserID); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RemoveBookFromShelf godoc
//
// @Summary     Remove a book from a shelf
// @Description Removes a book from the specified shelf.
// @Tags        shelves
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       shelf_id    path  string  true  "Shelf UUID"
// @Param       book_id     path  string  true  "Book UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/shelves/{shelf_id}/books/{book_id} [delete]
func (h *ShelfHandler) RemoveBookFromShelf(w http.ResponseWriter, r *http.Request) {
	shelfID, err := uuid.Parse(r.PathValue("shelf_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid shelf id")
		return
	}
	bookID, err := uuid.Parse(r.PathValue("book_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}
	if err := h.svc.RemoveBookFromShelf(r.Context(), shelfID, bookID); errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "book not on shelf")
		return
	} else if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Tags ─────────────────────────────────────────────────────────────────────

// ListTags godoc
//
// @Summary     List tags in a library
// @Description Returns all tags defined in the library.
// @Tags        tags
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Success     200  {array}   responses.TagResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/tags [get]
func (h *ShelfHandler) ListTags(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	tags, err := h.svc.ListTags(r.Context(), libraryID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(tags))
	for _, t := range tags {
		out = append(out, tagBody(t))
	}
	respond.JSON(w, http.StatusOK, out)
}

// CreateTag godoc
//
// @Summary     Create a tag
// @Description Creates a new tag in the library.
// @Tags        tags
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       body        body      object{name=string,color=string}  true  "Tag details"
// @Success     201  {object}  responses.TagResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/tags [post]
func (h *ShelfHandler) CreateTag(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())

	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tag, err := h.svc.CreateTag(r.Context(), libraryID, claims.UserID, body.Name, body.Color)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, tagBody(tag))
}

// UpdateTag godoc
//
// @Summary     Update a tag
// @Description Updates the name and/or color of a tag.
// @Tags        tags
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       tag_id      path      string  true  "Tag UUID"
// @Param       body        body      object{name=string,color=string}  true  "Updated tag"
// @Success     200  {object}  responses.TagResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/tags/{tag_id} [put]
func (h *ShelfHandler) UpdateTag(w http.ResponseWriter, r *http.Request) {
	tagID, err := uuid.Parse(r.PathValue("tag_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid tag id")
		return
	}
	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tag, err := h.svc.UpdateTag(r.Context(), tagID, body.Name, body.Color)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "tag not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, tagBody(tag))
}

// DeleteTag godoc
//
// @Summary     Delete a tag
// @Description Permanently deletes a tag from the library.
// @Tags        tags
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       tag_id      path  string  true  "Tag UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/tags/{tag_id} [delete]
func (h *ShelfHandler) DeleteTag(w http.ResponseWriter, r *http.Request) {
	tagID, err := uuid.Parse(r.PathValue("tag_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid tag id")
		return
	}
	if err := h.svc.DeleteTag(r.Context(), tagID); errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "tag not found")
		return
	} else if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

type shelfRequestBody struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Color        string   `json:"color"`
	Icon         string   `json:"icon"`
	DisplayOrder int      `json:"display_order"`
	TagIDStrings []string `json:"tag_ids"`
	TagIDs       []uuid.UUID
}

func decodeShelfRequest(r *http.Request) (*shelfRequestBody, error) {
	var body shelfRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, errors.New("invalid request body")
	}
	if body.Name == "" {
		return nil, errors.New("name is required")
	}
	if body.TagIDStrings != nil {
		body.TagIDs = make([]uuid.UUID, 0, len(body.TagIDStrings))
		for _, s := range body.TagIDStrings {
			id, err := uuid.Parse(s)
			if err != nil {
				return nil, errors.New("invalid tag_id: " + s)
			}
			body.TagIDs = append(body.TagIDs, id)
		}
	}
	return &body, nil
}

func tagsToBodyShelves(tags []*models.Tag) []map[string]any {
	out := make([]map[string]any, 0, len(tags))
	for _, t := range tags {
		out = append(out, map[string]any{"id": t.ID, "name": t.Name, "color": t.Color})
	}
	return out
}

func shelfBody(s *models.Shelf) map[string]any {
	tags := s.Tags
	if tags == nil {
		tags = []*models.Tag{}
	}
	return map[string]any{
		"id":            s.ID,
		"library_id":    s.LibraryID,
		"name":          s.Name,
		"description":   s.Description,
		"color":         s.Color,
		"icon":          s.Icon,
		"display_order": s.DisplayOrder,
		"book_count":    s.BookCount,
		"tags":          tagsToBodyShelves(tags),
		"created_at":    s.CreatedAt,
		"updated_at":    s.UpdatedAt,
	}
}

func tagBody(t *models.Tag) map[string]any {
	return map[string]any{
		"id":         t.ID,
		"library_id": t.LibraryID,
		"name":       t.Name,
		"color":      t.Color,
		"created_at": t.CreatedAt,
	}
}

