// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

type UserViewHandler struct {
	views *repository.UserViewRepo
}

func NewUserViewHandler(views *repository.UserViewRepo) *UserViewHandler {
	return &UserViewHandler{views: views}
}

func callerOf(r *http.Request) uuid.UUID {
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		return claims.UserID
	}
	return uuid.Nil
}

// ListMyViews godoc
//
// @Summary     List my saved views
// @Description Every view belonging to the caller, in display order. The built-ins are seeded on the first call, so a new account gets them without the client having to write them.
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Success     200  {object}  object{items=[]object{id=string,name=string,params=string,layout=string,icon=string,built_in=bool,hidden=bool,permanent=bool,display_order=int}}
// @Failure     401  {object}  object{error=string}
// @Router      /me/views [get]
func (h *UserViewHandler) ListMyViews(w http.ResponseWriter, r *http.Request) {
	views, err := h.views.List(r.Context(), callerOf(r))
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": views})
}

// viewBody is the shape a client sends. built_in, hidden and permanent are
// deliberately absent: they describe views we ship, and letting a request set
// them would let anyone mint an undeletable view or disguise their own as one.
type viewBody struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Params       string  `json:"params"`
	Layout       string  `json:"layout"`
	Icon         *string `json:"icon"`
	DisplayOrder int     `json:"display_order"`
}

// SaveMyView godoc
//
// @Summary     Create or update one of my views
// @Description Writes a view under the caller's account, replacing one with the same id. Used for saving a new view, renaming, and updating the Default so Books opens where you want.
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body      object{id=string,name=string,params=string,layout=string,icon=string,display_order=int}  true  "The view"
// @Success     200  {object}  object{id=string,name=string,params=string,layout=string,icon=string,display_order=int}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /me/views [put]
func (h *UserViewHandler) SaveMyView(w http.ResponseWriter, r *http.Request) {
	var body viewBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ID == "" || body.Name == "" {
		respond.Error(w, http.StatusBadRequest, "id and name are required")
		return
	}
	if body.Layout != "grid" && body.Layout != "rows" {
		respond.Error(w, http.StatusBadRequest, "layout must be grid or rows")
		return
	}

	caller := callerOf(r)

	// The flags come from whatever is already stored, never from the request,
	// so editing the Default cannot clear the permanence that keeps Books with
	// something to open on.
	existing, err := h.views.List(r.Context(), caller)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	v := repository.UserView{
		ID: body.ID, Name: body.Name, Params: body.Params,
		Layout: body.Layout, Icon: body.Icon, DisplayOrder: body.DisplayOrder,
	}
	for _, e := range existing {
		if e.ID == body.ID {
			v.BuiltIn, v.Hidden, v.Permanent = e.BuiltIn, e.Hidden, e.Permanent
			break
		}
	}
	if err := h.views.Upsert(r.Context(), caller, v); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, v)
}

// DeleteMyView godoc
//
// @Summary     Delete one of my views
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       view_id  path  string  true  "View id"
// @Success     204
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Failure     409  {object}  object{error=string}
// @Router      /me/views/{view_id} [delete]
func (h *UserViewHandler) DeleteMyView(w http.ResponseWriter, r *http.Request) {
	err := h.views.Delete(r.Context(), callerOf(r), r.PathValue("view_id"))
	switch {
	case errors.Is(err, repository.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "view not found")
	case errors.Is(err, repository.ErrViewPermanent):
		respond.Error(w, http.StatusConflict, "this view cannot be deleted")
	case err != nil:
		respond.ServerError(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// ImportMyViews godoc
//
// @Summary     Import views from a client's local storage
// @Description One-time migration for readers whose views were saved in the browser before they lived on the server. Views whose id already exists are left alone, so running it twice changes nothing.
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body      object{items=[]object{id=string,name=string,params=string,layout=string,icon=string,display_order=int}}  true  "Views to import"
// @Success     200  {object}  object{imported=int,skipped=int}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /me/views/import [post]
func (h *UserViewHandler) ImportMyViews(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []viewBody `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	caller := callerOf(r)
	existing, err := h.views.List(r.Context(), caller)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	have := make(map[string]bool, len(existing))
	for _, e := range existing {
		have[e.ID] = true
	}

	imported, skipped := 0, 0
	for _, item := range body.Items {
		// Skipping rather than overwriting: the seeded built-ins are already
		// here, and a browser's copy of "Reading now" carries nothing worth
		// preferring over the server's. Only views the server has never heard
		// of are the reader's own work.
		if item.ID == "" || have[item.ID] {
			skipped++
			continue
		}
		layout := item.Layout
		if layout != "grid" && layout != "rows" {
			layout = "grid"
		}
		if err := h.views.Upsert(r.Context(), caller, repository.UserView{
			ID: item.ID, Name: item.Name, Params: item.Params,
			Layout: layout, Icon: item.Icon, DisplayOrder: item.DisplayOrder,
		}); err != nil {
			respond.ServerError(w, r, err)
			return
		}
		imported++
	}
	respond.JSON(w, http.StatusOK, map[string]any{"imported": imported, "skipped": skipped})
}
