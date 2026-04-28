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

type LibraryHandler struct {
	svc *service.LibraryService
}

func NewLibraryHandler(svc *service.LibraryService) *LibraryHandler {
	return &LibraryHandler{svc: svc}
}

// ─── Library CRUD ─────────────────────────────────────────────────────────────

// ListLibraries godoc
//
// @Summary     List libraries
// @Description Returns all libraries the authenticated user has access to.
// @Tags        libraries
// @Produce     json
// @Security    BearerAuth
// @Success     200  {array}   responses.LibraryResponse
// @Failure     401  {object}  object{error=string}
// @Router      /libraries [get]
func (h *LibraryHandler) ListLibraries(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	libs, err := h.svc.ListLibraries(r.Context(), claims.UserID, claims.IsInstanceAdmin)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(libs))
	for _, l := range libs {
		out = append(out, libraryBody(l))
	}
	respond.JSON(w, http.StatusOK, out)
}

// CreateLibrary godoc
//
// @Summary     Create a library
// @Description Creates a new library owned by the authenticated user.
// @Tags        libraries
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body      object{name=string,description=string,slug=string,is_public=boolean}  true  "Library details"
// @Success     201   {object}  responses.LibraryResponse
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Failure     409   {object}  object{error=string}
// @Router      /libraries [post]
func (h *LibraryHandler) CreateLibrary(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())

	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Slug        string `json:"slug"`
		IsPublic    bool   `json:"is_public"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" {
		respond.Error(w, http.StatusBadRequest, "name is required")
		return
	}

	lib, err := h.svc.CreateLibrary(r.Context(), claims.UserID, service.CreateLibraryRequest{
		Name:        body.Name,
		Description: body.Description,
		Slug:        body.Slug,
		IsPublic:    body.IsPublic,
	})
	if errors.Is(err, repository.ErrDuplicate) {
		respond.Error(w, http.StatusConflict, "slug already in use")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, libraryBody(lib))
}

// GetLibrary godoc
//
// @Summary     Get a library
// @Description Returns details for a specific library.
// @Tags        libraries
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Success     200  {object}  responses.LibraryResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id} [get]
func (h *LibraryHandler) GetLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	lib, err := h.svc.GetLibrary(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "library not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, libraryBody(lib))
}

// UpdateLibrary godoc
//
// @Summary     Update a library
// @Description Updates name, description, or visibility of a library.
// @Tags        libraries
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       body        body      object{name=string,description=string,is_public=boolean}  true  "Updated fields"
// @Success     200  {object}  responses.LibraryResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id} [put]
func (h *LibraryHandler) UpdateLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}

	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		IsPublic    bool   `json:"is_public"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" {
		respond.Error(w, http.StatusBadRequest, "name is required")
		return
	}

	lib, err := h.svc.UpdateLibrary(r.Context(), id, service.UpdateLibraryRequest{
		Name:        body.Name,
		Description: body.Description,
		IsPublic:    body.IsPublic,
	})
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "library not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, libraryBody(lib))
}

// DeleteLibrary godoc
//
// @Summary     Delete a library
// @Description Permanently deletes a library and all its contents.
// @Tags        libraries
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id} [delete]
func (h *LibraryHandler) DeleteLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	if err := h.svc.DeleteLibrary(r.Context(), id); errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "library not found")
		return
	} else if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Members ──────────────────────────────────────────────────────────────────

// ListMembers godoc
//
// @Summary     List library members
// @Description Returns all members of a library with their roles.
// @Tags        libraries
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true   "Library UUID"
// @Param       search      query     string  false  "Filter by username/display name"
// @Param       tag         query     string  false  "Filter by tag name"
// @Success     200  {array}   responses.MemberResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/members [get]
func (h *LibraryHandler) ListMembers(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	search := r.URL.Query().Get("search")
	tagFilter := r.URL.Query().Get("tag")
	members, err := h.svc.ListMembers(r.Context(), libraryID, search, tagFilter)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(members))
	for _, m := range members {
		out = append(out, memberBody(m))
	}
	respond.JSON(w, http.StatusOK, out)
}

// AddMember godoc
//
// @Summary     Add a member to a library
// @Description Adds an existing user to the library with the specified role.
// @Tags        libraries
// @Accept      json
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       body        body  object{user_id=string,role=string}  true  "Member details"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Failure     409  {object}  object{error=string}
// @Router      /libraries/{library_id}/members [post]
func (h *LibraryHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}

	var body struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	targetUserID, err := uuid.Parse(body.UserID)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid user_id")
		return
	}
	if body.Role == "" {
		body.Role = "library_viewer"
	}

	claims := middleware.ClaimsFromContext(r.Context())
	err = h.svc.AddMember(r.Context(), libraryID, targetUserID, claims.UserID, body.Role)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "user not found")
		return
	}
	if errors.Is(err, repository.ErrDuplicate) {
		respond.Error(w, http.StatusConflict, "user is already a member")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UpdateMemberRole godoc
//
// @Summary     Update a member's role
// @Description Changes the role of an existing library member.
// @Tags        libraries
// @Accept      json
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       user_id     path  string  true  "User UUID"
// @Param       body        body  object{role=string}  true  "New role"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/members/{user_id} [patch]
func (h *LibraryHandler) UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	targetUserID, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}

	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Role == "" {
		respond.Error(w, http.StatusBadRequest, "role is required")
		return
	}

	err = h.svc.UpdateMemberRole(r.Context(), libraryID, targetUserID, body.Role)
	if errors.Is(err, service.ErrCannotRemoveOwner) {
		respond.Error(w, http.StatusBadRequest, "cannot change the owner's role")
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "member not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RemoveMember godoc
//
// @Summary     Remove a member from a library
// @Description Removes a user's membership from a library. Cannot remove the owner.
// @Tags        libraries
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       user_id     path  string  true  "User UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/members/{user_id} [delete]
func (h *LibraryHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	targetUserID, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}

	err = h.svc.RemoveMember(r.Context(), libraryID, targetUserID)
	if errors.Is(err, service.ErrCannotRemoveOwner) {
		respond.Error(w, http.StatusBadRequest, "cannot remove the library owner")
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "member not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Response helpers ─────────────────────────────────────────────────────────

func libraryBody(l *models.Library) map[string]any {
	return map[string]any{
		"id":            l.ID,
		"name":          l.Name,
		"description":   l.Description,
		"slug":          l.Slug,
		"owner_id":      l.OwnerID,
		"is_public":     l.IsPublic,
		"created_at":    l.CreatedAt,
		"updated_at":    l.UpdatedAt,
		"book_count":    l.BookCount,
		"reading_count": l.ReadingCount,
		"read_count":    l.ReadCount,
	}
}

func tagsToBodyMembers(tags []*models.Tag) []map[string]any {
	out := make([]map[string]any, 0, len(tags))
	for _, t := range tags {
		out = append(out, map[string]any{"id": t.ID, "name": t.Name, "color": t.Color})
	}
	return out
}

func memberBody(m *models.LibraryMember) map[string]any {
	tags := m.Tags
	if tags == nil {
		tags = []*models.Tag{}
	}
	body := map[string]any{
		"user_id":      m.UserID,
		"username":     m.Username,
		"display_name": m.DisplayName,
		"email":        m.Email,
		"role_id":      m.RoleID,
		"role":         m.RoleName,
		"joined_at":    m.JoinedAt,
		"tags":         tagsToBodyMembers(tags),
	}
	if m.InvitedBy != nil {
		body["invited_by"] = m.InvitedBy
	}
	return body
}
