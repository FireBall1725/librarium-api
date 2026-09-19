// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

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

// KioskHandler serves kiosks (iPads on a library's wall), signing members in
// on them, and members' kiosk PINs. See plans/ipad-kiosk.md.
type KioskHandler struct {
	svc *service.KioskService
}

func NewKioskHandler(svc *service.KioskService) *KioskHandler {
	return &KioskHandler{svc: svc}
}

func callerFrom(r *http.Request) *service.Caller {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return nil
	}
	return &service.Caller{UserID: claims.UserID, TokenID: claims.JTI, FromToken: claims.FromToken}
}

// kioskError answers with the status that fits a kiosk error.
func kioskError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "kiosk not found")
	case errors.Is(err, service.ErrKioskSettings), errors.Is(err, service.ErrBadPIN):
		respond.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrNotAKiosk), errors.Is(err, service.ErrInteractiveOnly),
		errors.Is(err, service.ErrNotLibraryMember), errors.Is(err, service.ErrNotAKioskSession):
		respond.Error(w, http.StatusForbidden, err.Error())
	case errors.Is(err, service.ErrCodeExpired):
		respond.Error(w, http.StatusGone, err.Error())
	case errors.Is(err, service.ErrNoPIN), errors.Is(err, service.ErrWrongPIN):
		respond.Error(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, service.ErrPINLocked):
		respond.Error(w, http.StatusTooManyRequests, err.Error())
	default:
		respond.ServerError(w, r, err)
	}
}

// kioskSettingsBody is a kiosk's settings in a request. clock_24h is
// presence-aware on update: absent leaves it, null goes back to following the
// iPad's region.
type kioskSettingsBody struct {
	Name           *string `json:"name"`
	AllowAnonymous *bool   `json:"allow_anonymous"`
	AllowSignup    *bool   `json:"allow_signup"`
	ShowBorrower   *bool   `json:"show_borrower"`
	IdleSeconds    *int    `json:"idle_seconds"`
}

func parseKioskSettings(r *http.Request) (repository.KioskSettings, error) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return repository.KioskSettings{}, err
	}
	whole, _ := json.Marshal(raw)
	var b kioskSettingsBody
	if err := json.Unmarshal(whole, &b); err != nil {
		return repository.KioskSettings{}, err
	}
	s := repository.KioskSettings{
		Name: b.Name, AllowAnonymous: b.AllowAnonymous, AllowSignup: b.AllowSignup,
		ShowBorrower: b.ShowBorrower, IdleSeconds: b.IdleSeconds,
	}
	if v, ok := raw["clock_24h"]; ok {
		s.SetClock24h = true
		if string(v) != "null" {
			var c bool
			if err := json.Unmarshal(v, &c); err != nil {
				return repository.KioskSettings{}, err
			}
			s.Clock24h = &c
		}
	}
	return s, nil
}

// ── Admin ────────────────────────────────────────────────────────────────────

// ListKiosks godoc
//
// @Summary     List a library's kiosks
// @Tags        kiosks
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Success     200  {object}  object{items=[]models.Kiosk}
// @Failure     401  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Router      /libraries/{library_id}/kiosks [get]
func (h *KioskHandler) ListKiosks(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	items, err := h.svc.List(r.Context(), libraryID)
	if err != nil {
		kioskError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items})
}

// RegisterKiosk godoc
//
// @Summary     Register a kiosk
// @Description Adds an iPad kiosk to the library and mints its API token, scoped to looking things up and lending and returning books. The token is in this response and never again. Needs a signed-in session, not an API token. Anonymous borrowing and sign-up start off.
// @Tags        kiosks
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       body        body  object{name=string,clock_24h=boolean,allow_anonymous=boolean,allow_signup=boolean,show_borrower=boolean,idle_seconds=integer}  true  "Settings; name is required"
// @Success     201  {object}  object{kiosk=models.Kiosk,token=string}
// @Failure     400  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Router      /libraries/{library_id}/kiosks [post]
func (h *KioskHandler) RegisterKiosk(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	set, err := parseKioskSettings(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	k, token, err := h.svc.Register(r.Context(), callerFrom(r), libraryID, set)
	if err != nil {
		kioskError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, map[string]any{"kiosk": k, "token": token})
}

// UpdateKiosk godoc
//
// @Summary     Change a kiosk's settings
// @Description Only the fields sent change. clock_24h null goes back to following the iPad's region.
// @Tags        kiosks
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       kiosk_id    path  string  true  "Kiosk UUID"
// @Param       body        body  object{name=string,clock_24h=boolean,allow_anonymous=boolean,allow_signup=boolean,show_borrower=boolean,idle_seconds=integer}  true  "Settings to change"
// @Success     200  {object}  models.Kiosk
// @Failure     400  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/kiosks/{kiosk_id} [patch]
func (h *KioskHandler) UpdateKiosk(w http.ResponseWriter, r *http.Request) {
	libraryID, err1 := uuid.Parse(r.PathValue("library_id"))
	id, err2 := uuid.Parse(r.PathValue("kiosk_id"))
	if err1 != nil || err2 != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	set, err := parseKioskSettings(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	k, err := h.svc.Update(r.Context(), libraryID, id, set)
	if err != nil {
		kioskError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, k)
}

// DeleteKiosk godoc
//
// @Summary     Remove a kiosk
// @Description Revokes the kiosk's token and signs out anyone signed in on it.
// @Tags        kiosks
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       kiosk_id    path  string  true  "Kiosk UUID"
// @Success     204
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/kiosks/{kiosk_id} [delete]
func (h *KioskHandler) DeleteKiosk(w http.ResponseWriter, r *http.Request) {
	libraryID, err1 := uuid.Parse(r.PathValue("library_id"))
	id, err2 := uuid.Parse(r.PathValue("kiosk_id"))
	if err1 != nil || err2 != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.svc.Delete(r.Context(), libraryID, id); err != nil {
		kioskError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── The kiosk itself ─────────────────────────────────────────────────────────

func (h *KioskHandler) kiosk(w http.ResponseWriter, r *http.Request) (*models.Kiosk, bool) {
	k, err := h.svc.Me(r.Context(), callerFrom(r))
	if err != nil {
		kioskError(w, r, err)
		return nil, false
	}
	return k, true
}

// KioskMe godoc
//
// @Summary     The calling kiosk's settings
// @Description For the iPad, with its kiosk token.
// @Tags        kiosks
// @Produce     json
// @Security    BearerAuth
// @Success     200  {object}  models.Kiosk
// @Failure     403  {object}  object{error=string}
// @Router      /kiosk/me [get]
func (h *KioskHandler) KioskMe(w http.ResponseWriter, r *http.Request) {
	if k, ok := h.kiosk(w, r); ok {
		respond.JSON(w, http.StatusOK, k)
	}
}

// KioskMembers godoc
//
// @Summary     Who can sign in on this kiosk
// @Description The library's members for the kiosk's "who are you" picker. has_pin is false for members who can only sign in with their phone.
// @Tags        kiosks
// @Produce     json
// @Security    BearerAuth
// @Success     200  {object}  object{items=[]models.KioskMember}
// @Failure     403  {object}  object{error=string}
// @Router      /kiosk/members [get]
func (h *KioskHandler) KioskMembers(w http.ResponseWriter, r *http.Request) {
	k, ok := h.kiosk(w, r)
	if !ok {
		return
	}
	items, err := h.svc.Members(r.Context(), k)
	if err != nil {
		kioskError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items})
}

// NewSigninCode godoc
//
// @Summary     Get a sign-in code to show as a QR code
// @Description For the kiosk. The code lives 60 seconds; ask for a new one when it runs out. A member's phone approves it, and the kiosk polls GET /kiosk/signin-codes/{code} for the member's session.
// @Tags        kiosks
// @Produce     json
// @Security    BearerAuth
// @Success     201  {object}  object{code=string,expires_at=string}
// @Failure     403  {object}  object{error=string}
// @Router      /kiosk/signin-codes [post]
func (h *KioskHandler) NewSigninCode(w http.ResponseWriter, r *http.Request) {
	k, ok := h.kiosk(w, r)
	if !ok {
		return
	}
	code, expires, err := h.svc.NewCode(r.Context(), k)
	if err != nil {
		kioskError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, map[string]any{"code": code, "expires_at": expires})
}

// PollSigninCode godoc
//
// @Summary     Has the code been approved?
// @Description For the kiosk that made the code. status is pending, approved, expired or used. The first poll after approval hands over the member's session, a token that ends after 15 minutes, on sign-out, or on idle reset; after that the code is used.
// @Tags        kiosks
// @Produce     json
// @Security    BearerAuth
// @Param       code  path  string  true  "Sign-in code"
// @Success     200  {object}  service.CodeStatus
// @Failure     403  {object}  object{error=string}
// @Router      /kiosk/signin-codes/{code} [get]
func (h *KioskHandler) PollSigninCode(w http.ResponseWriter, r *http.Request) {
	k, ok := h.kiosk(w, r)
	if !ok {
		return
	}
	status, err := h.svc.PollCode(r.Context(), k, r.PathValue("code"))
	if err != nil {
		kioskError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, status)
}

type pinSigninRequest struct {
	UserID uuid.UUID `json:"user_id"`
	PIN    string    `json:"pin"`
}

// PINSignin godoc
//
// @Summary     Sign a member in on the kiosk with their PIN
// @Description For the kiosk. Returns the same member session as the phone sign-in. Five wrong PINs in five minutes lock that member out on this kiosk (429) until they age out.
// @Tags        kiosks
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body  object{user_id=string,pin=string}  true  "Member and PIN"
// @Success     200  {object}  service.KioskSession
// @Failure     401  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Failure     429  {object}  object{error=string}
// @Router      /kiosk/signin/pin [post]
func (h *KioskHandler) PINSignin(w http.ResponseWriter, r *http.Request) {
	k, ok := h.kiosk(w, r)
	if !ok {
		return
	}
	var req pinSigninRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	session, err := h.svc.PINSignin(r.Context(), k, req.UserID, req.PIN)
	if err != nil {
		kioskError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, session)
}

// ── The member ───────────────────────────────────────────────────────────────

// PreviewSigninCode godoc
//
// @Summary     Which kiosk a code is for
// @Description For the member's phone, before approving: the kiosk and library the code signs them in on. 410 when the code expired or was used, 403 when they're not a member of that library.
// @Tags        kiosks
// @Produce     json
// @Security    BearerAuth
// @Param       code  path  string  true  "Sign-in code from the QR code"
// @Success     200  {object}  service.CodePreview
// @Failure     403  {object}  object{error=string}
// @Failure     410  {object}  object{error=string}
// @Router      /kiosk/signin-codes/{code}/preview [get]
func (h *KioskHandler) PreviewSigninCode(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Preview(r.Context(), callerFrom(r), r.PathValue("code"))
	if err != nil {
		kioskError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, p)
}

// ApproveSigninCode godoc
//
// @Summary     Sign in on the kiosk showing this code
// @Description For the member's phone, with their signed-in session (not an API token). The kiosk picks the session up on its next poll.
// @Tags        kiosks
// @Security    BearerAuth
// @Param       code  path  string  true  "Sign-in code from the QR code"
// @Success     204
// @Failure     403  {object}  object{error=string}
// @Failure     410  {object}  object{error=string}
// @Router      /kiosk/signin-codes/{code}/approve [post]
func (h *KioskHandler) ApproveSigninCode(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Approve(r.Context(), callerFrom(r), r.PathValue("code")); err != nil {
		kioskError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// EndKioskSession godoc
//
// @Summary     Sign the member out of the kiosk
// @Description Called with the member's kiosk session on sign-out and on idle reset. The session token stops working at once.
// @Tags        kiosks
// @Security    BearerAuth
// @Success     204
// @Failure     403  {object}  object{error=string}
// @Router      /kiosk/session [delete]
func (h *KioskHandler) EndKioskSession(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.EndSession(r.Context(), callerFrom(r)); err != nil {
		kioskError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetKioskPIN godoc
//
// @Summary     Whether I have a kiosk PIN
// @Tags        kiosks
// @Produce     json
// @Security    BearerAuth
// @Success     200  {object}  object{set=boolean}
// @Router      /me/kiosk-pin [get]
func (h *KioskHandler) GetKioskPIN(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil {
		respond.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	set, err := h.svc.HasPIN(r.Context(), c.UserID)
	if err != nil {
		kioskError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"set": set})
}

// SetKioskPIN godoc
//
// @Summary     Set my kiosk PIN
// @Description 4 to 8 digits, stored hashed like a password. Needs a signed-in session, not an API token, so a kiosk session can't change it.
// @Tags        kiosks
// @Accept      json
// @Security    BearerAuth
// @Param       body  body  object{pin=string}  true  "The PIN"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Router      /me/kiosk-pin [put]
func (h *KioskHandler) SetKioskPIN(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PIN string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.SetPIN(r.Context(), callerFrom(r), body.PIN); err != nil {
		kioskError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ClearKioskPIN godoc
//
// @Summary     Remove my kiosk PIN
// @Description Needs a signed-in session, not an API token.
// @Tags        kiosks
// @Security    BearerAuth
// @Success     204
// @Failure     403  {object}  object{error=string}
// @Router      /me/kiosk-pin [delete]
func (h *KioskHandler) ClearKioskPIN(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.ClearPIN(r.Context(), callerFrom(r)); err != nil {
		kioskError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
