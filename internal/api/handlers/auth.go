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
)

type AuthHandler struct {
	auth  *service.AuthService
	prefs *repository.PreferencesRepo
}

func NewAuthHandler(svc *service.AuthService, prefs *repository.PreferencesRepo) *AuthHandler {
	return &AuthHandler{auth: svc, prefs: prefs}
}

// Register godoc
//
// @Summary     Register a new user
// @Description Creates a new user account. Registration may be disabled by instance config.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body  body      object{username=string,email=string,display_name=string,password=string}  true  "Registration request"
// @Success     201   {object}  object{access_token=string,refresh_token=string,expires_in=integer,user=object{id=string,username=string,email=string,display_name=string,is_instance_admin=boolean}}
// @Failure     400   {object}  object{error=string}
// @Failure     403   {object}  object{error=string}
// @Failure     409   {object}  object{error=string}
// @Router      /auth/register [post]
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
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

	resp, err := h.auth.Register(r.Context(), service.RegisterRequest{
		Username:    req.Username,
		Email:       req.Email,
		DisplayName: req.DisplayName,
		Password:    req.Password,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrRegistrationDisabled):
			respond.Error(w, http.StatusForbidden, "registration is disabled")
		case errors.Is(err, repository.ErrDuplicate):
			respond.Error(w, http.StatusConflict, err.Error())
		default:
			respond.ServerError(w, r, err)
		}
		return
	}

	respond.JSON(w, http.StatusCreated, authResponseBody(resp))
}

// Login godoc
//
// @Summary     Log in
// @Description Authenticates with username/email and password; returns tokens.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body  body      object{identifier=string,password=string}  true  "Login request"
// @Success     200   {object}  object{access_token=string,refresh_token=string,expires_in=integer,user=object{id=string,username=string,email=string,display_name=string,is_instance_admin=boolean}}
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Router      /auth/login [post]
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Identifier == "" || req.Password == "" {
		respond.Error(w, http.StatusBadRequest, "identifier and password are required")
		return
	}

	resp, err := h.auth.Login(r.Context(), service.LoginRequest{
		Identifier: req.Identifier,
		Password:   req.Password,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidCredentials), errors.Is(err, service.ErrAccountInactive):
			respond.Error(w, http.StatusUnauthorized, "invalid credentials")
		default:
			respond.ServerError(w, r, err)
		}
		return
	}

	respond.JSON(w, http.StatusOK, authResponseBody(resp))
}

// Refresh godoc
//
// @Summary     Refresh access token
// @Description Exchanges a valid refresh token for a new token pair.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body  body      object{refresh_token=string}  true  "Refresh request"
// @Success     200   {object}  object{access_token=string,refresh_token=string,expires_in=integer,user=object{id=string,username=string,email=string,display_name=string,is_instance_admin=boolean}}
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Router      /auth/refresh [post]
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RefreshToken == "" {
		respond.Error(w, http.StatusBadRequest, "refresh_token is required")
		return
	}

	resp, err := h.auth.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidCredentials),
			errors.Is(err, service.ErrTokenExpired),
			errors.Is(err, service.ErrTokenRevoked):
			respond.Error(w, http.StatusUnauthorized, "invalid or expired refresh token")
		default:
			respond.ServerError(w, r, err)
		}
		return
	}

	respond.JSON(w, http.StatusOK, authResponseBody(resp))
}

// Logout godoc
//
// @Summary     Log out
// @Description Revokes the current access token. No request body needed.
// @Tags        auth
// @Produce     json
// @Security    BearerAuth
// @Success     200  {object}  object{message=string}
// @Failure     401  {object}  object{error=string}
// @Router      /auth/logout [post]
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	if err := h.auth.Logout(r.Context(), claims.UserID, claims.JTI, claims.ExpiresAt); err != nil {
		respond.ServerError(w, r, err)
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

// UpdateMe godoc
//
// @Summary     Update current user profile
// @Description Updates the display name and email of the authenticated user.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body      object{display_name=string,email=string}  true  "Update profile request"
// @Success     200   {object}  responses.UserResponse
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Failure     409   {object}  object{error=string}
// @Router      /auth/me [put]
func (h *AuthHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.DisplayName == "" || req.Email == "" {
		respond.Error(w, http.StatusBadRequest, "display_name and email are required")
		return
	}

	user, err := h.auth.UpdateProfile(r.Context(), claims.UserID, req.DisplayName, req.Email)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrDuplicate):
			respond.Error(w, http.StatusConflict, err.Error())
		default:
			respond.ServerError(w, r, err)
		}
		return
	}

	respond.JSON(w, http.StatusOK, userBody(user))
}

// UpdatePassword godoc
//
// @Summary     Change password
// @Description Changes the password for the authenticated user.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body      object{current_password=string,new_password=string}  true  "Change password request"
// @Success     200   {object}  object{message=string}
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Router      /auth/me/password [put]
func (h *AuthHandler) UpdatePassword(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		respond.Error(w, http.StatusBadRequest, "current_password and new_password are required")
		return
	}

	if err := h.auth.UpdatePassword(r.Context(), claims.UserID, req.CurrentPassword, req.NewPassword); err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidCredentials):
			respond.Error(w, http.StatusUnauthorized, "current password is incorrect")
		default:
			respond.ServerError(w, r, err)
		}
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{"message": "password updated"})
}

// Me godoc
//
// @Summary     Get current user
// @Description Returns the profile of the authenticated user.
// @Tags        auth
// @Produce     json
// @Security    BearerAuth
// @Success     200  {object}  responses.UserResponse
// @Failure     401  {object}  object{error=string}
// @Router      /auth/me [get]
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	user, err := h.auth.Me(r.Context(), claims.UserID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	respond.JSON(w, http.StatusOK, userBody(user))
}

// SearchUsers godoc
//
// @Summary     Search users
// @Description Full-text search across users; returns minimal profile data. Query must be at least 2 characters.
// @Tags        auth
// @Produce     json
// @Security    BearerAuth
// @Param       q    query     string  true  "Search query (min 2 chars)"
// @Success     200  {array}   object{id=string,username=string,display_name=string,email=string}
// @Failure     401  {object}  object{error=string}
// @Router      /users [get]
func (h *AuthHandler) SearchUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < 2 {
		respond.JSON(w, http.StatusOK, []any{})
		return
	}

	users, err := h.auth.SearchUsers(r.Context(), q)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{
			"id":           u.ID,
			"username":     u.Username,
			"display_name": u.DisplayName,
			"email":        u.Email,
		})
	}
	respond.JSON(w, http.StatusOK, out)
}

func authResponseBody(resp *service.AuthResponse) map[string]any {
	return map[string]any{
		"access_token":  resp.AccessToken,
		"refresh_token": resp.RefreshToken,
		"expires_in":    resp.ExpiresIn,
		"user":          userBody(resp.User),
	}
}

func userBody(u *models.User) map[string]any {
	return map[string]any{
		"id":                u.ID,
		"username":          u.Username,
		"email":             u.Email,
		"display_name":      u.DisplayName,
		"is_instance_admin": u.IsInstanceAdmin,
	}
}

// GetPreferences godoc
//
// @Summary     Get user preferences
// @Description Returns the stored preferences key-value map for the current user.
// @Tags        auth
// @Produce     json
// @Security    BearerAuth
// @Success     200  {object}  object{prefs=object}
// @Failure     401  {object}  object{error=string}
// @Router      /auth/me/preferences [get]
func (h *AuthHandler) GetPreferences(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	prefs, err := h.prefs.Get(r.Context(), claims.UserID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"prefs": prefs})
}

// PatchPreferences godoc
//
// @Summary     Patch user preferences
// @Description Merges supplied key-value pairs into the stored preferences.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body      object{}  true  "Arbitrary key-value pairs to merge"
// @Success     200   {object}  object{message=string}
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Router      /auth/me/preferences [patch]
func (h *AuthHandler) PatchPreferences(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var patch map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(patch) == 0 {
		respond.JSON(w, http.StatusOK, map[string]string{"message": "ok"})
		return
	}
	if err := h.prefs.Merge(r.Context(), claims.UserID, patch); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"message": "ok"})
}
