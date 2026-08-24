// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

// CopyHandler serves the physical objects a library holds, and the places they
// live.
//
// One row per object, which is what a copy_count integer could never express:
// two copies of one printing can differ by condition, by location, and by
// whether one of them is currently at a friend's house.
type CopyHandler struct {
	copies    *repository.CopyRepo
	locations *repository.CopyLocationRepo
	roles     *repository.UserRoleRepo
}

func NewCopyHandler(copies *repository.CopyRepo, locations *repository.CopyLocationRepo, roles *repository.UserRoleRepo) *CopyHandler {
	return &CopyHandler{copies: copies, locations: locations, roles: roles}
}

func libraryIDOf(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("library_id"))
	return id, err == nil
}

// ListCopiesForBook godoc
//
// @Summary     List my copies of a book
// @Description Every copy of the work across the libraries the caller can reach, signed copies first. A copy with no edition means the printing was never recorded, which is a supported state.
// @Tags        copies
// @Produce     json
// @Security    BearerAuth
// @Param       book_id  path  string  true  "Book UUID"
// @Success     200  {object}  object{items=[]object{id=string,library_id=string,edition_id=string,condition=string,is_signed=bool,location_name=string,on_loan_to=string}}
// @Failure     401  {object}  object{error=string}
// @Router      /books/{book_id}/copies [get]
func (h *CopyHandler) ListCopiesForBook(w http.ResponseWriter, r *http.Request) {
	bookID, ok := bookIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}

	// Narrowed to what the caller can see rather than filtered afterwards: a
	// row that never reaches the query cannot leak from it.
	libs, err := h.roles.ReadableLibraryIDs(r.Context(), callerOf(r))
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	copies, err := h.copies.ListForBook(r.Context(), bookID, libs)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": copies})
}

// ListCopiesForLibrary godoc
//
// @Summary     List a library's copies
// @Tags        copies
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path   string  true   "Library UUID"
// @Param       limit       query  int     false  "Page size, default 50"
// @Param       offset      query  int     false  "Offset"
// @Success     200  {object}  object{items=[]object{id=string,book_id=string,condition=string,is_signed=bool}}
// @Failure     401  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Router      /libraries/{library_id}/copies [get]
func (h *CopyHandler) ListCopiesForLibrary(w http.ResponseWriter, r *http.Request) {
	libraryID, ok := libraryIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}

	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	offset := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = v
	}

	copies, err := h.copies.ListForLibrary(r.Context(), libraryID, limit, offset)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": copies})
}

type copyBody struct {
	BookID        string  `json:"book_id"`
	EditionID     *string `json:"edition_id"`
	AcquiredAt    *string `json:"acquired_at"`
	AcquiredFrom  string  `json:"acquired_from"`
	PriceMinor    *int64  `json:"price_minor"`
	PriceCurrency string  `json:"price_currency"`
	Condition     string  `json:"condition"`
	IsSigned      *bool   `json:"is_signed"`
	Notes         string  `json:"notes"`
	LocationID    *string `json:"location_id"`
}

// CreateCopy godoc
//
// @Summary     Record a copy
// @Description Adds one physical object to a library. Everything but the book is optional, so recording a book you own is one field and the detail only appears when someone wants it.
// @Tags        copies
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       body  body  object{book_id=string,edition_id=string,acquired_at=string,acquired_from=string,price_minor=int,price_currency=string,condition=string,is_signed=bool,notes=string,location_id=string}  true  "The copy"
// @Success     201  {object}  object{id=string,book_id=string,condition=string,is_signed=bool}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/copies [post]
func (h *CopyHandler) CreateCopy(w http.ResponseWriter, r *http.Request) {
	libraryID, ok := libraryIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}

	var body copyBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	bookID, err := uuid.Parse(body.BookID)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "a copy needs a book")
		return
	}

	in := repository.CreateCopyInput{
		LibraryID:     libraryID,
		BookID:        bookID,
		AcquiredAt:    body.AcquiredAt,
		AcquiredFrom:  body.AcquiredFrom,
		PriceMinor:    body.PriceMinor,
		PriceCurrency: body.PriceCurrency,
		Condition:     body.Condition,
		Notes:         body.Notes,
	}
	if caller := callerOf(r); caller != uuid.Nil {
		in.AcquiredBy = &caller
	}
	if body.IsSigned != nil {
		in.IsSigned = *body.IsSigned
	}
	if in.EditionID, err = optionalUUID(body.EditionID); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid edition id")
		return
	}
	if in.LocationID, err = optionalUUID(body.LocationID); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid location id")
		return
	}

	copy, err := h.copies.Create(r.Context(), in)
	if handled := respondCopyErr(w, err); handled {
		return
	}
	respond.JSON(w, http.StatusCreated, copy)
}

// UpdateCopy godoc
//
// @Summary     Update a copy
// @Description Partial update: a field left out is untouched.
// @Tags        copies
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       copy_id  path  string  true  "Copy UUID"
// @Param       body  body  object{edition_id=string,acquired_at=string,acquired_from=string,price_minor=int,price_currency=string,condition=string,is_signed=bool,notes=string,location_id=string}  true  "Changes"
// @Success     200  {object}  object{id=string,condition=string,is_signed=bool}
// @Failure     400  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /copies/{copy_id} [patch]
func (h *CopyHandler) UpdateCopy(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("copy_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid copy id")
		return
	}

	var body copyBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	in := repository.UpdateCopyInput{
		AcquiredFrom: nonEmpty(body.AcquiredFrom),
		Condition:    nonEmpty(body.Condition),
		Notes:        nonEmpty(body.Notes),
		IsSigned:     body.IsSigned,
	}
	if body.AcquiredAt != nil {
		in.AcquiredAt = &body.AcquiredAt
	}
	if body.PriceMinor != nil {
		in.PriceMinor = &body.PriceMinor
	}
	if body.PriceCurrency != "" {
		in.PriceCurrency = &body.PriceCurrency
	}
	if body.EditionID != nil {
		id, err := optionalUUID(body.EditionID)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid edition id")
			return
		}
		in.EditionID = &id
	}
	if body.LocationID != nil {
		id, err := optionalUUID(body.LocationID)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid location id")
			return
		}
		in.LocationID = &id
	}

	copy, err := h.copies.Update(r.Context(), id, in)
	if handled := respondCopyErr(w, err); handled {
		return
	}
	respond.JSON(w, http.StatusOK, copy)
}

// DeleteCopy godoc
//
// @Summary     Remove a copy
// @Description Soft-deletes, because losing an object by accident is the expensive case.
// @Tags        copies
// @Produce     json
// @Security    BearerAuth
// @Param       copy_id  path  string  true  "Copy UUID"
// @Success     204
// @Failure     404  {object}  object{error=string}
// @Router      /copies/{copy_id} [delete]
func (h *CopyHandler) DeleteCopy(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("copy_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid copy id")
		return
	}
	if err := h.copies.Delete(r.Context(), id); errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "copy not found")
		return
	} else if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListLocations godoc
//
// @Summary     List where a library keeps things
// @Description A tree, so office can contain top shelf without a second concept. Distinct from storage locations, which are filesystem paths for ebook files.
// @Tags        copies
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Success     200  {object}  object{items=[]object{id=string,name=string,parent_id=string,copy_count=int}}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/locations [get]
func (h *CopyHandler) ListLocations(w http.ResponseWriter, r *http.Request) {
	libraryID, ok := libraryIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	locations, err := h.locations.List(r.Context(), libraryID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": locations})
}

type locationBody struct {
	Name     string  `json:"name"`
	ParentID *string `json:"parent_id"`
}

// CreateLocation godoc
//
// @Summary     Add a place to keep things
// @Tags        copies
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       body  body  object{name=string,parent_id=string}  true  "The location"
// @Success     201  {object}  object{id=string,name=string,parent_id=string}
// @Failure     400  {object}  object{error=string}
// @Router      /libraries/{library_id}/locations [post]
func (h *CopyHandler) CreateLocation(w http.ResponseWriter, r *http.Request) {
	libraryID, ok := libraryIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}

	var body locationBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	parent, err := optionalUUID(body.ParentID)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid parent id")
		return
	}

	location, err := h.locations.Create(r.Context(), libraryID, body.Name, parent)
	switch {
	case errors.Is(err, repository.ErrLocationNotInLibrary):
		respond.Error(w, http.StatusBadRequest, "that parent belongs to a different library")
		return
	case errors.Is(err, repository.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "parent location not found")
		return
	case err != nil:
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusCreated, location)
}

// DeleteLocation godoc
//
// @Summary     Remove a place
// @Description Refused while copies are still filed there, because a copy whose location silently became null is a book you cannot find.
// @Tags        copies
// @Produce     json
// @Security    BearerAuth
// @Param       location_id  path  string  true  "Location UUID"
// @Success     204
// @Failure     409  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /locations/{location_id} [delete]
func (h *CopyHandler) DeleteLocation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("location_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid location id")
		return
	}

	err = h.locations.Delete(r.Context(), id)
	switch {
	case errors.Is(err, repository.ErrLocationInUse):
		respond.Error(w, http.StatusConflict, "that location still holds copies; move them first")
		return
	case errors.Is(err, repository.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "location not found")
		return
	case err != nil:
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// respondCopyErr maps the composite-key failures to messages that say what is
// actually wrong. Both constraints exist because a plain foreign key would have
// let the bad state through silently.
func respondCopyErr(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, repository.ErrEditionNotOfBook):
		respond.Error(w, http.StatusBadRequest, "that edition belongs to a different book")
	case errors.Is(err, repository.ErrLocationNotInLibrary):
		respond.Error(w, http.StatusBadRequest, "that location belongs to a different library")
	case errors.Is(err, repository.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "copy not found")
	default:
		respond.Error(w, http.StatusBadRequest, err.Error())
	}
	return true
}

// optionalUUID parses a pointer-to-string field, treating absent and empty as
// "not set" rather than as an error.
func optionalUUID(s *string) (*uuid.UUID, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
