// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/google/uuid"
)

type AdminHandler struct {
	svc *service.AuthService
}

func NewAdminHandler(svc *service.AuthService) *AdminHandler {
	return &AdminHandler{svc: svc}
}

// ListUsers godoc
//
// @Summary     List all users (admin)
// @Description Returns a paginated list of all instance users. Instance admin only.
// @Tags        admin
// @Produce     json
// @Security    BearerAuth
// @Param       page      query     integer  false  "Page number (default 1)"
// @Param       per_page  query     integer  false  "Items per page (default 20, max 100)"
// @Success     200  {object}  object{items=[]object,total=integer,page=integer,per_page=integer}
// @Failure     401  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Router      /admin/users [get]
func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 || perPage > 100 {
		perPage = 20
	}

	users, total, err := h.svc.ListUsers(r.Context(), page, perPage)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	items := make([]map[string]any, len(users))
	for i, u := range users {
		items[i] = adminUserBody(u)
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"items":    items,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

// CreateUser godoc
//
// @Summary     Create a user (admin)
// @Description Creates a new user account bypassing registration settings. Instance admin only.
// @Tags        admin
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body      object{username=string,email=string,display_name=string,password=string}  true  "Create user request"
// @Success     201   {object}  object{id=string,username=string,email=string,display_name=string,is_active=boolean,is_instance_admin=boolean,created_at=string,last_login_at=string}
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Failure     403   {object}  object{error=string}
// @Failure     409   {object}  object{error=string}
// @Router      /admin/users [post]
func (h *AdminHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string `json:"username"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" || req.Email == "" || req.Password == "" {
		respond.Error(w, http.StatusBadRequest, "username, email, and password are required")
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = req.Username
	}

	user, err := h.svc.AdminCreateUser(r.Context(), service.RegisterRequest{
		Username:    req.Username,
		Email:       req.Email,
		DisplayName: req.DisplayName,
		Password:    req.Password,
	})
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrDuplicate):
			respond.Error(w, http.StatusConflict, err.Error())
		default:
			respond.ServerError(w, r, err)
		}
		return
	}

	respond.JSON(w, http.StatusCreated, adminUserBody(user))
}

// UpdateUser godoc
//
// @Summary     Update a user (admin)
// @Description Partially updates a user's profile or admin/active status. Instance admin only.
// @Tags        admin
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       id    path      string  true  "User UUID"
// @Param       body  body      object{display_name=string,email=string,is_active=boolean,is_instance_admin=boolean}  false  "Fields to update"
// @Success     200   {object}  object{id=string,username=string,email=string,display_name=string,is_active=boolean,is_instance_admin=boolean,created_at=string,last_login_at=string}
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Failure     403   {object}  object{error=string}
// @Failure     404   {object}  object{error=string}
// @Router      /admin/users/{id} [patch]
func (h *AdminHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}

	var req struct {
		DisplayName     *string `json:"display_name"`
		Email           *string `json:"email"`
		IsActive        *bool   `json:"is_active"`
		IsInstanceAdmin *bool   `json:"is_instance_admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	callerID := uuid.Nil
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		callerID = claims.UserID
	}

	user, err := h.svc.AdminPatchUser(r.Context(), id, callerID, service.UserPatch{
		DisplayName:     req.DisplayName,
		Email:           req.Email,
		IsActive:        req.IsActive,
		IsInstanceAdmin: req.IsInstanceAdmin,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrSelfDeactivate):
			respond.Error(w, http.StatusBadRequest, "cannot deactivate your own account")
		case errors.Is(err, service.ErrSelfDemote):
			respond.Error(w, http.StatusBadRequest, "cannot remove your own admin privileges")
		case errors.Is(err, repository.ErrNotFound):
			respond.Error(w, http.StatusNotFound, "user not found")
		case errors.Is(err, repository.ErrDuplicate):
			respond.Error(w, http.StatusConflict, err.Error())
		default:
			respond.ServerError(w, r, err)
		}
		return
	}

	respond.JSON(w, http.StatusOK, adminUserBody(user))
}

// DeleteUser godoc
//
// @Summary     Delete a user (admin)
// @Description Permanently deletes a user account. Instance admin only. Cannot delete yourself.
// @Tags        admin
// @Produce     json
// @Security    BearerAuth
// @Param       id  path      string  true  "User UUID"
// @Success     200  {object}  object{message=string}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /admin/users/{id} [delete]
func (h *AdminHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	targetID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}

	callerID := uuid.Nil
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		callerID = claims.UserID
	}

	if err := h.svc.AdminDeleteUser(r.Context(), targetID, callerID); err != nil {
		switch {
		case errors.Is(err, service.ErrSelfDelete):
			respond.Error(w, http.StatusBadRequest, "cannot delete your own account")
		case errors.Is(err, repository.ErrNotFound):
			respond.Error(w, http.StatusNotFound, "user not found")
		default:
			respond.ServerError(w, r, err)
		}
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{"message": "user deleted"})
}

func adminUserBody(u *models.User) map[string]any {
	return map[string]any{
		"id":                u.ID,
		"username":          u.Username,
		"email":             u.Email,
		"display_name":      u.DisplayName,
		"is_active":         u.IsActive,
		"is_instance_admin": u.IsInstanceAdmin,
		"created_at":        u.CreatedAt,
		"last_login_at":     u.LastLoginAt,
	}
}
