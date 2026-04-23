// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/fireball1725/librarium-api/internal/ai"
	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/service"
)

// ollamaModelsResponse wraps the Ollama /api/tags models list. Named for
// swag — inline object{models=[]ai.OllamaModel} can't resolve the package.
type ollamaModelsResponse struct {
	Models []ai.OllamaModel `json:"models"`
}

// osaurusModelsResponse wraps the Osaurus /v1/models list. Same rationale
// as ollamaModelsResponse.
type osaurusModelsResponse struct {
	Models []ai.OsaurusModel `json:"models"`
}

// AIHandler groups admin-side AI endpoints: provider CRUD, test, active
// selection, and permissions policy. User-scoped endpoints (opt-in, taste
// profile) live on AIUserHandler so routing stays grouped by authorization.
type AIHandler struct {
	svc *service.AIService
}

func NewAIHandler(svc *service.AIService) *AIHandler {
	return &AIHandler{svc: svc}
}

// ListProviders godoc
//
//	@Summary     List AI providers (admin)
//	@Description Returns provider status, masked config, active flag, and config-field schema for each registered AI provider.
//	@Tags        admin,ai
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {array}   service.AIProviderStatus
//	@Failure     401  {object}  object{error=string}
//	@Failure     403  {object}  object{error=string}
//	@Router      /admin/connections/ai [get]
func (h *AIHandler) ListProviders(w http.ResponseWriter, r *http.Request) {
	statuses, err := h.svc.GetAllProviderStatus(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, statuses)
}

// ConfigureProvider godoc
//
//	@Summary     Configure an AI provider (admin)
//	@Description Updates the API key, model, base URL, or enabled flag for the named provider. Secrets are merged over existing config so omitted keys are preserved.
//	@Tags        admin,ai
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       provider  path      string    true  "Provider name"
//	@Param       body      body      object{}  true  "Provider configuration key-value pairs"
//	@Success     200       {array}   service.AIProviderStatus
//	@Failure     400       {object}  object{error=string}
//	@Router      /admin/connections/ai/{provider} [put]
func (h *AIHandler) ConfigureProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	if name == "" {
		respond.Error(w, http.StatusBadRequest, "provider name is required")
		return
	}
	var cfg map[string]string
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.ConfigureProvider(r.Context(), name, cfg); err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	statuses, err := h.svc.GetAllProviderStatus(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, statuses)
}

// TestProvider godoc
//
//	@Summary     Test an AI provider (admin)
//	@Description Performs a cheap probe call to verify the provider's API key and model.
//	@Tags        admin,ai
//	@Produce     json
//	@Security    BearerAuth
//	@Param       provider  path      string  true  "Provider name"
//	@Success     200       {object}  object{ok=boolean,reply=string,error=string}
//	@Router      /admin/connections/ai/{provider}/test [post]
func (h *AIHandler) TestProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	if name == "" {
		respond.Error(w, http.StatusBadRequest, "provider name is required")
		return
	}
	reply, err := h.svc.TestProvider(r.Context(), name)
	if err != nil {
		respond.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"ok": true, "reply": reply})
}

// SetActiveProvider godoc
//
//	@Summary     Set the active AI provider (admin)
//	@Description Exactly one AI provider is active at a time. Passing an empty name disables AI suggestions.
//	@Tags        admin,ai
//	@Accept      json
//	@Security    BearerAuth
//	@Param       body  body      object{provider=string}  true  "Provider name"
//	@Success     204
//	@Failure     400  {object}  object{error=string}
//	@Router      /admin/connections/ai/active [post]
func (h *AIHandler) SetActiveProvider(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.SetActiveProvider(r.Context(), body.Provider); err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetPermissions godoc
//
//	@Summary     Get AI data-access permissions (admin)
//	@Description Returns the deployment-wide data categories the AI may access. Combined restrictive-wins with each user's opt-in.
//	@Tags        admin,ai
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {object}  service.AIPermissions
//	@Router      /admin/connections/ai/permissions [get]
func (h *AIHandler) GetPermissions(w http.ResponseWriter, r *http.Request) {
	perms, err := h.svc.GetPermissions(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, perms)
}

// SetPermissions godoc
//
//	@Summary     Update AI data-access permissions (admin)
//	@Description Replaces the deployment-wide data-access policy. All fields must be provided; omitted fields default to false.
//	@Tags        admin,ai
//	@Accept      json
//	@Security    BearerAuth
//	@Param       body  body      service.AIPermissions  true  "Permissions object"
//	@Success     200   {object}  service.AIPermissions
//	@Failure     400   {object}  object{error=string}
//	@Router      /admin/connections/ai/permissions [put]
func (h *AIHandler) SetPermissions(w http.ResponseWriter, r *http.Request) {
	var perms service.AIPermissions
	if err := json.NewDecoder(r.Body).Decode(&perms); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.SetPermissions(r.Context(), perms); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, perms)
}

// ListOllamaModels godoc
//
//	@Summary     List locally-pulled Ollama models (admin)
//	@Description Proxies the configured Ollama host's /api/tags endpoint. Used by the provider-config UI to render a model dropdown instead of a free-text input. Returns 503 when the host is unreachable so the UI can fall back to the text input.
//	@Tags        admin,ai
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {object}  ollamaModelsResponse
//	@Failure     503  {object}  object{error=string,reachable=boolean}
//	@Router      /admin/connections/ai/ollama/models [get]
func (h *AIHandler) ListOllamaModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.svc.ListOllamaModels(r.Context())
	if err != nil {
		respond.JSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":     err.Error(),
			"reachable": false,
		})
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"models": models})
}

// ListOsaurusModels godoc
//
//	@Summary     List models available on the configured Osaurus host (admin)
//	@Description Proxies the configured Osaurus host's /v1/models endpoint (OpenAI shape). Used by the provider-config UI to render a model dropdown instead of a free-text input. Returns 503 when the host is unreachable so the UI can fall back to the text input.
//	@Tags        admin,ai
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {object}  osaurusModelsResponse
//	@Failure     503  {object}  object{error=string,reachable=boolean}
//	@Router      /admin/connections/ai/osaurus/models [get]
func (h *AIHandler) ListOsaurusModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.svc.ListOsaurusModels(r.Context())
	if err != nil {
		respond.JSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":     err.Error(),
			"reachable": false,
		})
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"models": models})
}

// ─── User-scoped AI endpoints ─────────────────────────────────────────────────

// AIUserHandler groups the /me endpoints for AI opt-in and taste profile.
type AIUserHandler struct {
	svc *service.AIUserService
}

func NewAIUserHandler(svc *service.AIUserService) *AIUserHandler {
	return &AIUserHandler{svc: svc}
}

// GetPrefs godoc
//
//	@Summary     Get my AI preferences
//	@Description Returns whether the caller has opted in to AI features.
//	@Tags        me,ai
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {object}  service.UserAIPrefsView
//	@Router      /me/ai-prefs [get]
func (h *AIUserHandler) GetPrefs(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	prefs, err := h.svc.GetPrefs(r.Context(), claims.UserID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, prefs)
}

// UpdatePrefs godoc
//
//	@Summary     Update my AI preferences
//	@Description Sets whether the caller is opted in to AI features.
//	@Tags        me,ai
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body  body      object{opt_in=boolean}  true  "Opt-in flag"
//	@Success     200   {object}  service.UserAIPrefsView
//	@Failure     400   {object}  object{error=string}
//	@Router      /me/ai-prefs [put]
func (h *AIUserHandler) UpdatePrefs(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var body struct {
		OptIn bool `json:"opt_in"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.SetOptIn(r.Context(), claims.UserID, body.OptIn); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, service.UserAIPrefsView{OptIn: body.OptIn})
}

// GetTasteProfile godoc
//
//	@Summary     Get my taste profile
//	@Description Returns the JSON taste profile the user has saved. Empty object if none saved.
//	@Tags        me,ai
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {object}  object{}
//	@Router      /me/taste-profile [get]
func (h *AIUserHandler) GetTasteProfile(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	taste, err := h.svc.GetTasteProfile(r.Context(), claims.UserID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	// Write raw JSON bytes straight through so we don't double-encode.
	w.Header().Set("Content-Type", "application/json")
	if len(taste) == 0 {
		_, _ = w.Write([]byte("{}"))
		return
	}
	_, _ = w.Write(taste)
}

// UpdateTasteProfile godoc
//
//	@Summary     Update my taste profile
//	@Description Replaces the saved taste profile with the supplied JSON object.
//	@Tags        me,ai
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body  body      object{}  true  "Taste profile JSON object"
//	@Success     200   {object}  object{}
//	@Failure     400   {object}  object{error=string}
//	@Router      /me/taste-profile [put]
func (h *AIUserHandler) UpdateTasteProfile(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.svc.SetTasteProfile(r.Context(), claims.UserID, raw); err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}
