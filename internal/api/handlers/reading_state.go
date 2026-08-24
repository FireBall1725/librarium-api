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
	"github.com/google/uuid"
)

// ReadingStateHandler serves what one person thinks of a work, and the log of
// their passes through it.
//
// The routes are keyed to the book rather than to a library and an edition,
// which is the visible half of moving reading state to the work: there is no
// longer a library_id or an edition_id in the path, because an opinion belongs
// to neither.
type ReadingStateHandler struct {
	userBooks *repository.UserBookRepo
	sessions  *repository.ReadingSessionRepo
}

func NewReadingStateHandler(userBooks *repository.UserBookRepo, sessions *repository.ReadingSessionRepo) *ReadingStateHandler {
	return &ReadingStateHandler{userBooks: userBooks, sessions: sessions}
}

func bookIDOf(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("book_id"))
	return id, err == nil
}

// callerOf is who is asking. uuid.Nil when nobody is, which only happens off an
// authenticated route: every caller of this sits behind requireAuth, so a Nil
// here matches no rows rather than matching every row.
func callerOf(r *http.Request) uuid.UUID {
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		return claims.UserID
	}
	return uuid.Nil
}

// GetMyBook godoc
//
// @Summary     Get my reading state for a book
// @Description The caller's status, rating, review and notes for a work. Reads through containment, so a volume inside an omnibus the caller has read comes back as read with inherited set.
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       book_id  path  string  true  "Book UUID"
// @Success     200  {object}  object{book_id=string,read_status=string,rating=int,is_favorite=bool,review=string,notes=string,wants=bool,inherited=bool}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /books/{book_id}/me [get]
func (h *ReadingStateHandler) GetMyBook(w http.ResponseWriter, r *http.Request) {
	bookID, ok := bookIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}

	ub, err := h.userBooks.GetEffective(r.Context(), callerOf(r), bookID)
	if errors.Is(err, repository.ErrNotFound) {
		// Nothing said about it is not an error, it is the default state, and
		// returning 404 would make every unread book look like a broken link.
		respond.JSON(w, http.StatusOK, map[string]any{
			"book_id": bookID, "read_status": "unread", "is_favorite": false,
			"review": "", "notes": "", "wants": false, "inherited": false,
		})
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, readingStateBody(ub))
}

// readingStateBody keeps the wire shape in one place. An inherited row carries
// a status and nothing else, because a rating is an opinion about the thing
// rated and never moves through containment.
func readingStateBody(ub *models.UserBook) map[string]any {
	return map[string]any{
		"book_id":     ub.BookID,
		"read_status": ub.ReadStatus,
		"rating":      ub.Rating,
		"is_favorite": ub.IsFavorite,
		"review":      ub.Review,
		"notes":       ub.Notes,
		"wants":       ub.Wants,
		"inherited":   ub.Inherited,
	}
}

// myBookBody is what a client sends. Every field is a pointer so an omitted one
// means "leave it alone": a client that only knows about favourites must not
// blank a review typed on another device.
type myBookBody struct {
	ReadStatus  *string `json:"read_status"`
	Rating      *int    `json:"rating"`
	ClearRating bool    `json:"clear_rating"`
	IsFavorite  *bool   `json:"is_favorite"`
	Review      *string `json:"review"`
	Notes       *string `json:"notes"`
	Wants       *bool   `json:"wants"`
}

// PutMyBook godoc
//
// @Summary     Set my reading state for a book
// @Description Partial update. Any field left out is untouched, which is what lets two clients edit different fields without either losing. Send clear_rating to remove a rating, since omitting it means "no change".
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       book_id  path  string  true  "Book UUID"
// @Param       body  body  object{read_status=string,rating=int,clear_rating=bool,is_favorite=bool,review=string,notes=string,wants=bool}  true  "Reading state"
// @Success     200  {object}  object{book_id=string,read_status=string,rating=int,is_favorite=bool,review=string,notes=string,wants=bool}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /books/{book_id}/me [put]
func (h *ReadingStateHandler) PutMyBook(w http.ResponseWriter, r *http.Request) {
	bookID, ok := bookIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}

	var body myBookBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	in := repository.UpsertInput{
		ReadStatus: body.ReadStatus,
		IsFavorite: body.IsFavorite,
		Review:     body.Review,
		Notes:      body.Notes,
		Wants:      body.Wants,
	}
	// Clearing a rating and not mentioning one are different requests, and a
	// single nullable field on the wire cannot say both.
	switch {
	case body.ClearRating:
		var none *int
		in.Rating = &none
	case body.Rating != nil:
		in.Rating = &body.Rating
	}

	ub, err := h.userBooks.Upsert(r.Context(), callerOf(r), bookID, in)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, readingStateBody(ub))
}

// DeleteMyBook godoc
//
// @Summary     Forget my reading state for a book
// @Description Soft-deletes the caller's opinion. The review and notes history tables keep the text, so this is recoverable.
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       book_id  path  string  true  "Book UUID"
// @Success     204
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /books/{book_id}/me [delete]
func (h *ReadingStateHandler) DeleteMyBook(w http.ResponseWriter, r *http.Request) {
	bookID, ok := bookIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}

	err := h.userBooks.Delete(r.Context(), callerOf(r), bookID)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "no reading state to forget")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListSessions godoc
//
// @Summary     List my reading sessions for a book
// @Description Each pass through the work, most recent first. A reread is a separate session rather than a counter, so it can say when and in which printing.
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       book_id  path  string  true  "Book UUID"
// @Success     200  {object}  object{items=[]object{id=string,book_id=string,edition_id=string,started_at=string,finished_at=string,status=string,progress_unit=string,progress_value=number}}
// @Failure     401  {object}  object{error=string}
// @Router      /books/{book_id}/sessions [get]
func (h *ReadingStateHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	bookID, ok := bookIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}

	sessions, err := h.sessions.ListForBook(r.Context(), callerOf(r), bookID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": sessions})
}

type sessionBody struct {
	EditionID     *string  `json:"edition_id"`
	StartedAt     *string  `json:"started_at"`
	FinishedAt    *string  `json:"finished_at"`
	Status        string   `json:"status"`
	ProgressUnit  string   `json:"progress_unit"`
	ProgressValue *float64 `json:"progress_value"`
}

// CreateSession godoc
//
// @Summary     Log a reading session
// @Description Records one pass through a work. Progress is a unit and a value rather than a blob; page progress requires an edition, because page 200 of a paperback is not page 200 of the omnibus.
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       book_id  path  string  true  "Book UUID"
// @Param       body  body  object{edition_id=string,started_at=string,finished_at=string,status=string,progress_unit=string,progress_value=number}  true  "The session"
// @Success     201  {object}  object{id=string,book_id=string,status=string}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /books/{book_id}/sessions [post]
func (h *ReadingStateHandler) CreateSession(w http.ResponseWriter, r *http.Request) {
	bookID, ok := bookIDOf(r)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}

	var body sessionBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	in := repository.CreateSessionInput{
		UserID: callerOf(r), BookID: bookID,
		StartedAt: body.StartedAt, FinishedAt: body.FinishedAt,
		Status: body.Status, ProgressUnit: body.ProgressUnit, ProgressValue: body.ProgressValue,
	}
	if body.EditionID != nil && *body.EditionID != "" {
		id, err := uuid.Parse(*body.EditionID)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid edition id")
			return
		}
		in.EditionID = &id
	}

	session, err := h.sessions.Create(r.Context(), in)
	switch {
	case errors.Is(err, repository.ErrPageNeedsEdition):
		respond.Error(w, http.StatusBadRequest,
			"page progress needs an edition, since a page number only means something in one printing")
		return
	case errors.Is(err, repository.ErrEditionNotOfBook):
		respond.Error(w, http.StatusBadRequest, "that edition belongs to a different book")
		return
	case errors.Is(err, repository.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "book or edition not found")
		return
	case err != nil:
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusCreated, session)
}

type updateSessionBody struct {
	EditionID     *string  `json:"edition_id"`
	StartedAt     *string  `json:"started_at"`
	FinishedAt    *string  `json:"finished_at"`
	Status        *string  `json:"status"`
	ProgressUnit  *string  `json:"progress_unit"`
	ProgressValue *float64 `json:"progress_value"`
}

// UpdateSession godoc
//
// @Summary     Correct a logged reading session
// @Description Partial update. A field left out is untouched; send it as null to clear it, which is how a mistyped finish date is removed rather than overwritten.
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       session_id  path  string  true  "Session UUID"
// @Param       body  body  object{edition_id=string,started_at=string,finished_at=string,status=string,progress_unit=string,progress_value=number}  true  "The changes"
// @Success     200  {object}  object{id=string,book_id=string,started_at=string,finished_at=string,status=string}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /sessions/{session_id} [patch]
func (h *ReadingStateHandler) UpdateSession(w http.ResponseWriter, r *http.Request) {
	sessionID, err := uuid.Parse(r.PathValue("session_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid session id")
		return
	}

	// Decoding into a map as well as the struct is what separates "sent as
	// null" from "not sent at all". A pointer alone cannot tell them apart, and
	// they mean different things here: one clears a date, the other keeps it.
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var body updateSessionBody
	for key, target := range map[string]any{
		"edition_id": &body.EditionID, "started_at": &body.StartedAt,
		"finished_at": &body.FinishedAt, "status": &body.Status,
		"progress_unit": &body.ProgressUnit, "progress_value": &body.ProgressValue,
	} {
		if v, ok := raw[key]; ok {
			if err := json.Unmarshal(v, target); err != nil {
				respond.Error(w, http.StatusBadRequest, "invalid value for "+key)
				return
			}
		}
	}

	sent := func(key string) bool { _, ok := raw[key]; return ok }
	in := repository.UpdateSessionInput{
		StartedAt:     body.StartedAt,
		ClearStarted:  sent("started_at") && body.StartedAt == nil,
		FinishedAt:    body.FinishedAt,
		ClearFinished: sent("finished_at") && body.FinishedAt == nil,
		Status:        body.Status,
		ProgressUnit:  body.ProgressUnit,
		ProgressValue: body.ProgressValue,
		ClearEdition:  sent("edition_id") && body.EditionID == nil,
	}
	if body.EditionID != nil && *body.EditionID != "" {
		id, err := uuid.Parse(*body.EditionID)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid edition id")
			return
		}
		in.EditionID = &id
	}

	if !h.ownsSession(w, r, sessionID) {
		return
	}

	session, err := h.sessions.Update(r.Context(), sessionID, in)
	switch {
	case errors.Is(err, repository.ErrPageNeedsEdition):
		respond.Error(w, http.StatusBadRequest,
			"page progress needs an edition, since page 200 of a paperback is not page 200 of the omnibus")
		return
	case errors.Is(err, repository.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "session not found")
		return
	case err != nil:
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, session)
}

// ownsSession refuses a session belonging to somebody else.
//
// A session id is not a capability. Sessions are addressed by their own id
// rather than under a book, so the ownership check cannot come from the path
// the way it does everywhere else, and without this anyone holding an id could
// read or destroy someone else's reading history.
//
// One implementation on purpose: the update and delete paths both need it, and
// two copies of an authorisation check is how one of them drifts.
func (h *ReadingStateHandler) ownsSession(w http.ResponseWriter, r *http.Request, sessionID uuid.UUID) bool {
	existing, err := h.sessions.FindByID(r.Context(), sessionID)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "session not found")
		return false
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return false
	}
	if existing.UserID != callerOf(r) {
		// 404 rather than 403: saying "forbidden" would confirm the session
		// exists to somebody who cannot see it.
		respond.Error(w, http.StatusNotFound, "session not found")
		return false
	}
	return true
}

// DeleteSession godoc
//
// @Summary     Delete a reading session
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       session_id  path  string  true  "Session UUID"
// @Success     204
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /sessions/{session_id} [delete]
func (h *ReadingStateHandler) DeleteSession(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("session_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid session id")
		return
	}

	if !h.ownsSession(w, r, id) {
		return
	}

	if err := h.sessions.Delete(r.Context(), id); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
