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

// ListHandler serves lists and the wishlist.
//
// Lists are what shelves and saved views became. They were never distinguished
// by ownership but by how membership is decided, so one concept with a kind
// gives one sharing implementation instead of two.
type ListHandler struct {
	lists    *repository.ListRepo
	wishlist *repository.WishlistRepo
}

func NewListHandler(lists *repository.ListRepo, wishlist *repository.WishlistRepo) *ListHandler {
	return &ListHandler{lists: lists, wishlist: wishlist}
}

// ListMyLists godoc
//
// @Summary     List my lists
// @Description The caller's own lists plus any shared into a library they can reach. A manual list enumerates its books; a smart one computes them from a stored filter.
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Success     200  {object}  object{items=[]object{id=string,name=string,kind=string,visibility=string,book_count=int}}
// @Failure     401  {object}  object{error=string}
// @Router      /me/lists [get]
func (h *ListHandler) ListMyLists(w http.ResponseWriter, r *http.Request) {
	// Seed on read rather than at sign-up: an account made before this release
	// never got them, and there is no other moment every user passes through.
	// The insert is idempotent, so calling it on every read costs seven
	// no-op statements and saves carrying a "has been seeded" flag.
	if err := h.lists.SeedBuiltIns(r.Context(), callerOf(r)); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	lists, err := h.lists.ListForUser(r.Context(), callerOf(r))
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": lists})
}

// ListsHoldingBook godoc
//
// @Summary     List my lists that hold a book
// @Description Which of the caller's lists a book is on. Manual lists only: a smart list's membership is whatever its filter matches right now, and answering for one would mean running every stored filter to draw a label.
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       book_id  path  string  true  "Book UUID"
// @Success     200  {object}  object{items=[]object{id=string,name=string,icon=string,color=string,visibility=string}}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /books/{book_id}/lists [get]
func (h *ListHandler) ListsHoldingBook(w http.ResponseWriter, r *http.Request) {
	bookID, ok := bookIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}
	items, err := h.lists.ContainingBook(r.Context(), callerOf(r), bookID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items})
}

type listBody struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Icon            string          `json:"icon"`
	Color           string          `json:"color"`
	Kind            string          `json:"kind"`
	Filter          json.RawMessage `json:"filter"`
	Layout          string          `json:"layout"`
	Visibility      string          `json:"visibility"`
	SharedLibraryID *string         `json:"shared_library_id"`
}

// CreateList godoc
//
// @Summary     Create a list
// @Description A manual list is filled by hand; a smart list stores a filter and fills itself. Visibility is private, library or public; a public list is given an unguessable share token, since on a public link the token is the only credential.
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body  object{name=string,description=string,icon=string,color=string,kind=string,filter=object,layout=string,visibility=string,shared_library_id=string}  true  "The list"
// @Success     201  {object}  object{id=string,name=string,kind=string,visibility=string,share_token=string}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /me/lists [post]
func (h *ListHandler) CreateList(w http.ResponseWriter, r *http.Request) {
	var body listBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	in := repository.CreateListInput{
		OwnerUserID: callerOf(r),
		Name:        body.Name,
		Description: body.Description,
		Icon:        body.Icon,
		Color:       body.Color,
		Kind:        body.Kind,
		Filter:      body.Filter,
		Layout:      body.Layout,
		Visibility:  body.Visibility,
	}
	if body.SharedLibraryID != nil && *body.SharedLibraryID != "" {
		id, err := uuid.Parse(*body.SharedLibraryID)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid library id")
			return
		}
		in.SharedLibraryID = &id
	}

	list, err := h.lists.Create(r.Context(), in)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusCreated, list)
}

// SetListOrder godoc
//
// @Summary     Arrange my own view rail
// @Description Records where the caller wants each view, including views shared
// @Description with them. Order belongs to a rail rather than to a view, so
// @Description this changes nothing for anyone else. Send every id, in order.
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body  object{ids=[]string}  true  "Every visible list id, in the order wanted"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /me/lists/order [put]
func (h *ListHandler) SetListOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ids := make([]uuid.UUID, 0, len(body.IDs))
	for _, raw := range body.IDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid list id")
			return
		}
		ids = append(ids, id)
	}

	if err := h.lists.SetOrder(r.Context(), callerOf(r), ids); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteList godoc
//
// @Summary     Delete one of my lists
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       list_id  path  string  true  "List UUID"
// @Success     204
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /me/lists/{list_id} [delete]
func (h *ListHandler) DeleteList(w http.ResponseWriter, r *http.Request) {
	list, ok := h.ownedList(w, r)
	if !ok {
		return
	}
	err := h.lists.Delete(r.Context(), list.ID)
	switch {
	case errors.Is(err, repository.ErrListPermanent):
		respond.Error(w, http.StatusConflict,
			"that list ships with Librarium and cannot be deleted")
		return
	case errors.Is(err, repository.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "list not found")
		return
	case err != nil:
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type updateListBody struct {
	Name         *string         `json:"name"`
	Description  *string         `json:"description"`
	Icon         *string         `json:"icon"`
	Color        *string         `json:"color"`
	Layout       *string         `json:"layout"`
	DisplayOrder *int            `json:"display_order"`
	Filter       json.RawMessage `json:"filter"`
	// Sharing. Visibility moves the list; SharedLibraryID names where to, and
	// is required when moving to 'library'.
	Visibility      *string `json:"visibility"`
	SharedLibraryID *string `json:"shared_library_id"`
}

// UpdateList godoc
//
// @Summary     Change one of my lists
// @Description Also moves a list between private, shared with a library and public. Sharing into a library needs a role on it, and a public list keeps the token it already has so links already handed out keep working.
// @Description Only the keys present are changed, so renaming a list does not require sending the rest of it back. Kind is absent on purpose: a manual list cannot become smart without its enumerated books becoming a lie.
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       list_id  path  string  true  "List UUID"
// @Param       body  body  object{name=string,description=string,icon=string,color=string,layout=string,display_order=int,filter=object}  true  "The changes"
// @Success     200  {object}  object{id=string,name=string,kind=string,visibility=string,book_count=int}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /me/lists/{list_id} [patch]
func (h *ListHandler) UpdateList(w http.ResponseWriter, r *http.Request) {
	list, ok := h.ownedList(w, r)
	if !ok {
		return
	}
	var body updateListBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	in := repository.UpdateListInput{
		Name:         body.Name,
		Description:  body.Description,
		Icon:         body.Icon,
		Color:        body.Color,
		Layout:       body.Layout,
		DisplayOrder: body.DisplayOrder,
		Filter:       body.Filter,
		Visibility:   body.Visibility,
	}
	if body.SharedLibraryID != nil && *body.SharedLibraryID != "" {
		libraryID, err := uuid.Parse(*body.SharedLibraryID)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid library id")
			return
		}
		// Checked here rather than trusted from the body. The library id
		// arrives from the client, so without this, sharing would be a way to
		// put a row into the rail of a library the sender cannot reach.
		allowed, err := h.lists.CanShareInto(r.Context(), callerOf(r), libraryID)
		if err != nil {
			respond.ServerError(w, r, err)
			return
		}
		if !allowed {
			respond.Error(w, http.StatusForbidden,
				"you cannot share a list into a library you do not belong to")
			return
		}
		in.SharedLibraryID = &libraryID
	}

	updated, err := h.lists.Update(r.Context(), list.ID, in)
	switch {
	case errors.Is(err, repository.ErrSmartListNotEnumerable):
		respond.Error(w, http.StatusBadRequest,
			"only a smart list has a filter to change")
		return
	case errors.Is(err, repository.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "list not found")
		return
	case err != nil:
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, updated)
}

// AddBookToList godoc
//
// @Summary     Put a book on a list
// @Description Manual lists only: a smart list computes its own membership, so adding by hand would produce a row its filter disagrees with. Allowed for the list's owner, or for anyone holding shelves:update on the library a shared list belongs to.
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       list_id  path  string  true  "List UUID"
// @Param       book_id  path  string  true  "Book UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /me/lists/{list_id}/books/{book_id} [post]
func (h *ListHandler) AddBookToList(w http.ResponseWriter, r *http.Request) {
	list, ok := h.editableList(w, r)
	if !ok {
		return
	}
	bookID, valid := bookIDOf(r)
	if !valid {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}

	err := h.lists.AddBook(r.Context(), list.ID, bookID, 0)
	switch {
	case errors.Is(err, repository.ErrSmartListNotEnumerable):
		respond.Error(w, http.StatusBadRequest,
			"that list fills itself from its filter, so books cannot be added by hand")
		return
	case err != nil:
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RemoveBookFromList godoc
//
// @Summary     Take a book off a list
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       list_id  path  string  true  "List UUID"
// @Param       book_id  path  string  true  "Book UUID"
// @Success     204
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /me/lists/{list_id}/books/{book_id} [delete]
func (h *ListHandler) RemoveBookFromList(w http.ResponseWriter, r *http.Request) {
	list, ok := h.editableList(w, r)
	if !ok {
		return
	}
	bookID, valid := bookIDOf(r)
	if !valid {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}

	err := h.lists.RemoveBook(r.Context(), list.ID, bookID)
	if errors.Is(err, repository.ErrSmartListNotEnumerable) {
		respond.Error(w, http.StatusBadRequest, "that list fills itself from its filter")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// editableList resolves the list in the path and checks the caller may change
// what is on it: they own it, or they hold shelves:update on the library it is
// shared into.
//
// Separate from ownedList because filing a book is not the same act as renaming
// or deleting. A shared list nobody but its author could add to would be a list
// only in name.
func (h *ListHandler) editableList(w http.ResponseWriter, r *http.Request) (*listRef, bool) {
	id, err := uuid.Parse(r.PathValue("list_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid list id")
		return nil, false
	}
	allowed, err := h.lists.Editable(r.Context(), callerOf(r), id)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && !allowed) {
		// 404 rather than 403, for the same reason ownedList does: a 403 would
		// confirm the list exists to someone who cannot see it.
		respond.Error(w, http.StatusNotFound, "list not found")
		return nil, false
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return nil, false
	}
	return &listRef{ID: id}, true
}

// ownedList resolves the list in the path and checks the caller owns it.
//
// Ownership rather than visibility on purpose: a list shared into a library is
// readable by its members but still belongs to one person, and letting a reader
// edit it would make "shared with you" mean "yours".
func (h *ListHandler) ownedList(w http.ResponseWriter, r *http.Request) (*listRef, bool) {
	id, err := uuid.Parse(r.PathValue("list_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid list id")
		return nil, false
	}
	list, err := h.lists.FindByID(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && list.OwnerUserID != callerOf(r)) {
		// 404 rather than 403: a 403 would confirm the list exists to someone
		// who cannot see it.
		respond.Error(w, http.StatusNotFound, "list not found")
		return nil, false
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return nil, false
	}
	return &listRef{ID: list.ID}, true
}

type listRef struct{ ID uuid.UUID }

// ListMyWishlist godoc
//
// @Summary     List what I want
// @Description One list holding both shapes: entries pointing at a catalogue book, and free-text entries for things the catalogue has never heard of.
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Success     200  {object}  object{items=[]object{id=string,book_id=string,title=string,author_name=string,notes=string,priority=int}}
// @Failure     401  {object}  object{error=string}
// @Router      /me/wishlist [get]
func (h *ListHandler) ListMyWishlist(w http.ResponseWriter, r *http.Request) {
	items, err := h.wishlist.List(r.Context(), callerOf(r))
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items})
}

type wishlistBody struct {
	BookID     *string `json:"book_id"`
	Title      string  `json:"title"`
	AuthorName string  `json:"author_name"`
	Notes      string  `json:"notes"`
	Priority   int     `json:"priority"`
}

// AddToWishlist godoc
//
// @Summary     Want something
// @Description Send a book_id for something in the catalogue, or a title for something that is not. Both land in the same list, so asking what you want is one query rather than a union.
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body  object{book_id=string,title=string,author_name=string,notes=string,priority=int}  true  "The want"
// @Success     201  {object}  object{id=string,book_id=string,title=string}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     409  {object}  object{error=string}
// @Router      /me/wishlist [post]
func (h *ListHandler) AddToWishlist(w http.ResponseWriter, r *http.Request) {
	var body wishlistBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	caller := callerOf(r)
	var entry any
	var err error

	if body.BookID != nil && *body.BookID != "" {
		id, parseErr := uuid.Parse(*body.BookID)
		if parseErr != nil {
			respond.Error(w, http.StatusBadRequest, "invalid book id")
			return
		}
		entry, err = h.wishlist.AddCatalogued(r.Context(), caller, id, body.Notes, body.Priority)
	} else {
		entry, err = h.wishlist.AddFreeText(r.Context(), caller, body.Title, body.AuthorName, body.Notes, body.Priority)
	}

	switch {
	case errors.Is(err, repository.ErrAlreadyWanted):
		respond.Error(w, http.StatusConflict, "that book is already on your wishlist")
		return
	case errors.Is(err, repository.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "book not found")
		return
	case err != nil:
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusCreated, entry)
}

// RemoveFromWishlist godoc
//
// @Summary     Stop wanting something
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       entry_id  path  string  true  "Wishlist entry UUID"
// @Success     204
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /me/wishlist/{entry_id} [delete]
func (h *ListHandler) RemoveFromWishlist(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("entry_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid entry id")
		return
	}

	// Scoped to the caller in the query itself, so one person's id cannot
	// delete another person's want.
	if err := h.wishlist.Remove(r.Context(), callerOf(r), id); errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "wishlist entry not found")
		return
	} else if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
