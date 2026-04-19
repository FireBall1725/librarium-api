// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/fireball1725/librarium-api/internal/service"
)

type SetupHandler struct {
	auth  *service.AuthService
	users *repository.UserRepo
}

func NewSetupHandler(auth *service.AuthService, users *repository.UserRepo) *SetupHandler {
	return &SetupHandler{auth: auth, users: users}
}

// Status godoc
//
// @Summary     Setup status
// @Description Returns whether the instance has been initialized (at least one user exists).
//
//	Used by clients to decide whether to show a setup wizard instead of the login screen.
//
// @Tags        setup
// @Produce     json
// @Success     200  {object}  object{initialized=boolean}
// @Router      /setup/status [get]
func (h *SetupHandler) Status(w http.ResponseWriter, r *http.Request) {
	count, err := h.users.Count(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"initialized": count > 0})
}

// BootstrapAdmin godoc
//
// @Summary     Create first instance admin
// @Description Creates the initial instance admin on a fresh install. Fails with 409 once any user exists.
// @Tags        setup
// @Accept      json
// @Produce     json
// @Param       body  body      object{username=string,email=string,display_name=string,password=string}  true  "Admin account request"
// @Success     201   {object}  object{access_token=string,refresh_token=string,expires_in=integer,user=object}
// @Failure     400   {object}  object{error=string}
// @Failure     409   {object}  object{error=string}
// @Router      /setup/admin [post]
func (h *SetupHandler) BootstrapAdmin(w http.ResponseWriter, r *http.Request) {
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

	resp, err := h.auth.BootstrapAdmin(r.Context(), service.RegisterRequest{
		Username:    req.Username,
		Email:       req.Email,
		DisplayName: req.DisplayName,
		Password:    req.Password,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAlreadyInitialized):
			respond.Error(w, http.StatusConflict, "instance is already initialized")
		case errors.Is(err, repository.ErrDuplicate):
			respond.Error(w, http.StatusConflict, err.Error())
		default:
			respond.ServerError(w, r, err)
		}
		return
	}

	respond.JSON(w, http.StatusCreated, authResponseBody(resp))
}
