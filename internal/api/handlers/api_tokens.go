// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

// APITokenHandler serves the `/me/api-tokens` endpoints used by the web UI
// to mint, list, and revoke personal access tokens for the current user.
type APITokenHandler struct {
	tokens *repository.APITokenRepo
}

func NewAPITokenHandler(tokens *repository.APITokenRepo) *APITokenHandler {
	return &APITokenHandler{tokens: tokens}
}

// ─── Wire shape ─────────────────────────────────────────────────────────────

// tokenView is the safe public shape of an api_tokens row. Omits the hash;
// includes the suffix so the UI can render `lbrm_pat_•••XXXX` to help the
// user identify which token is which.
type tokenView struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	TokenSuffix string     `json:"token_suffix"`
	Scopes      []string   `json:"scopes"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func toTokenView(t *models.APIToken) tokenView {
	return tokenView{
		ID:          t.ID,
		Name:        t.Name,
		TokenSuffix: t.TokenSuffix,
		Scopes:      t.Scopes,
		LastUsedAt:  t.LastUsedAt,
		ExpiresAt:   t.ExpiresAt,
		RevokedAt:   t.RevokedAt,
		CreatedAt:   t.CreatedAt,
	}
}

// createTokenResponse is the ONLY response shape where the raw token appears.
// The server intentionally has no endpoint to retrieve a token after this.
type createTokenResponse struct {
	tokenView
	Token string `json:"token"`
}

type createTokenRequest struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// ─── Handlers ───────────────────────────────────────────────────────────────

// List godoc
//
//	@Summary     List the caller's API tokens
//	@Description Returns every personal access token minted by the caller, including revoked ones, newest first. Raw token values are never returned; only metadata plus a four-character suffix.
//	@Tags        me,api-tokens
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {array}   handlers.tokenView
//	@Router      /me/api-tokens [get]
func (h *APITokenHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rows, err := h.tokens.ListByUser(r.Context(), claims.UserID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]tokenView, 0, len(rows))
	for _, t := range rows {
		out = append(out, toTokenView(t))
	}
	respond.JSON(w, http.StatusOK, out)
}

// Create godoc
//
//	@Summary     Mint a new personal access token
//	@Description Creates a personal access token for the caller. The raw token value is returned in this response exactly once; the server stores only a sha256 hash and cannot retrieve it again. Lost tokens must be revoked and remintered.
//	@Tags        me,api-tokens
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body  body      handlers.createTokenRequest  true  "Token metadata"
//	@Success     201   {object}  handlers.createTokenResponse
//	@Failure     400   {object}  object{error=string}
//	@Router      /me/api-tokens [post]
func (h *APITokenHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	// Minting new tokens requires an interactive session. Blocks a leaked
	// PAT from self-perpetuating by spawning siblings, regardless of its
	// scope set.
	if claims.FromToken {
		respond.Error(w, http.StatusForbidden, "api tokens can only be minted from an interactive session")
		return
	}

	var body createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.Name) == 0 || len(body.Name) > 64 {
		respond.Error(w, http.StatusBadRequest, "name is required (1-64 chars)")
		return
	}

	// Normalize scopes: drop empties and dupes, preserve order.
	scopes := normalizeScopes(body.Scopes)

	minted, err := repository.Generate(claims.UserID, body.Name, scopes, body.ExpiresAt)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	if err := h.tokens.Create(r.Context(), minted.Token); err != nil {
		respond.ServerError(w, r, err)
		return
	}

	respond.JSON(w, http.StatusCreated, createTokenResponse{
		tokenView: toTokenView(minted.Token),
		Token:     minted.Raw,
	})
}

// Revoke godoc
//
//	@Summary     Revoke one of the caller's API tokens
//	@Description Soft-deletes the token. Revocation takes effect immediately; subsequent auth attempts with the revoked token 401.
//	@Tags        me,api-tokens
//	@Security    BearerAuth
//	@Param       id   path  string  true  "Token UUID"
//	@Success     204
//	@Failure     404  {object}  object{error=string}
//	@Router      /me/api-tokens/{id} [delete]
func (h *APITokenHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.tokens.Revoke(r.Context(), claims.UserID, id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "token not found")
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── helpers ────────────────────────────────────────────────────────────────

// normalizeScopes strips empties and duplicates while preserving order.
func normalizeScopes(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
