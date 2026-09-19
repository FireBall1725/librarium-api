// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/providers"
	"github.com/fireball1725/librarium-api/internal/service"
)

type ProviderHandler struct {
	svc *service.ProviderService
}

func NewProviderHandler(svc *service.ProviderService) *ProviderHandler {
	return &ProviderHandler{svc: svc}
}

// ListProviders godoc
//
// @Summary     List metadata providers (admin)
// @Description Returns the status and configuration of all registered metadata providers.
// @Tags        admin,providers
// @Produce     json
// @Security    BearerAuth
// @Success     200  {array}   service.ProviderStatus
// @Failure     401  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Router      /admin/providers [get]
func (h *ProviderHandler) ListProviders(w http.ResponseWriter, r *http.Request) {
	statuses, err := h.svc.GetAllProviderStatus(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, statuses)
}

// ConfigureProvider godoc
//
// @Summary     Configure a metadata provider (admin)
// @Description Updates the API key or settings for the named provider.
// @Tags        admin,providers
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       name  path      string            true  "Provider name"
// @Param       body  body      object{}          true  "Provider configuration key-value pairs"
// @Success     200   {array}   service.ProviderStatus
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Failure     403   {object}  object{error=string}
// @Router      /admin/providers/{name} [put]
func (h *ProviderHandler) ConfigureProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
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
// @Summary     Test a metadata provider (admin)
// @Description Performs a live test lookup to verify provider credentials are working.
// @Tags        admin,providers
// @Produce     json
// @Security    BearerAuth
// @Param       name  path      string  true  "Provider name"
// @Success     200   {object}  object{ok=boolean,title=string}
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Failure     403   {object}  object{error=string}
// @Router      /admin/providers/{name}/test [post]
func (h *ProviderHandler) TestProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		respond.Error(w, http.StatusBadRequest, "provider name is required")
		return
	}
	title, err := h.svc.TestProvider(r.Context(), name)
	if err != nil {
		respond.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"ok": true, "title": title})
}

// LookupISBN godoc
//
// @Summary     Lookup book by ISBN
// @Description Queries all enabled providers and returns per-provider results.
// @Tags        lookup
// @Produce     json
// @Security    BearerAuth
// @Param       isbn  path      string  true  "ISBN-10 or ISBN-13"
// @Success     200   {array}   providers.BookResult
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Router      /lookup/isbn/{isbn} [get]
func (h *ProviderHandler) LookupISBN(w http.ResponseWriter, r *http.Request) {
	isbn := r.PathValue("isbn")
	if isbn == "" {
		respond.Error(w, http.StatusBadRequest, "isbn is required")
		return
	}

	results := h.svc.LookupISBN(r.Context(), isbn)
	if results == nil {
		results = []*providers.BookResult{}
	}
	respond.JSON(w, http.StatusOK, results)
}

// LookupUPC godoc
//
// @Summary     Lookup book by UPC or EAN
// @Description Queries every enabled provider that can look up a UPC-A or non-ISBN EAN-13 and returns per-provider results. Send 12 or 13 digits, plus the 5-digit add-on when the scanner read one: on comics the add-on picks the issue and the variant. A result that carries an ISBN can be added through the normal ISBN path.
// @Tags        lookup
// @Produce     json
// @Security    BearerAuth
// @Param       code  path      string  true  "UPC-A or EAN-13, 12 or 13 digits, optionally followed by a 5-digit add-on"
// @Success     200   {array}   providers.BookResult
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Router      /lookup/upc/{code} [get]
func (h *ProviderHandler) LookupUPC(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !isUPCOrEAN(code) {
		respond.Error(w, http.StatusBadRequest, "code must be 12 or 13 digits, optionally followed by a 5-digit add-on")
		return
	}

	results := h.svc.LookupUPC(r.Context(), code)
	if results == nil {
		results = []*providers.BookResult{}
	}
	respond.JSON(w, http.StatusOK, results)
}

// isUPCOrEAN checks the shape only. The check digit is the client's job, and a
// provider that doesn't know the code just returns nothing. The add-on is
// kept because a comic's 12 digits are shared by a whole run of issues.
func isUPCOrEAN(code string) bool {
	switch len(code) {
	case 12, 13, 17, 18:
	default:
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// LookupISBNMerged godoc
//
// @Summary     Lookup ISBN merged
// @Description Asks every enabled provider at once and returns one merged result. Each field has a pre-selected value, the reason it was chosen (agreed, only, longest, most_detail, first), every provider that gave it, and the other values. Covers come largest first, and providers says who answered, who had no record and who missed the deadline.
// @Tags        lookup
// @Produce     json
// @Security    BearerAuth
// @Param       isbn  path      string  true  "ISBN-10 or ISBN-13"
// @Success     200   {object}  providers.MergedBookResult
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Router      /lookup/isbn/{isbn}/merged [get]
func (h *ProviderHandler) LookupISBNMerged(w http.ResponseWriter, r *http.Request) {
	isbn := r.PathValue("isbn")
	if isbn == "" {
		respond.Error(w, http.StatusBadRequest, "isbn is required")
		return
	}
	merged, err := h.svc.LookupISBNMerged(r.Context(), isbn)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, merged)
}

// GetProviderOrder godoc
//
// @Summary     Get provider priority order (admin)
// @Description Returns the saved provider order. Kept for older clients; lookups no longer use it, each field is pre-selected from the answers.
// @Tags        admin,providers
// @Produce     json
// @Security    BearerAuth
// @Success     200  {object}  object{order=[]string}
// @Failure     401  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Router      /admin/providers/order [get]
func (h *ProviderHandler) GetProviderOrder(w http.ResponseWriter, r *http.Request) {
	order, err := h.svc.GetProviderOrder(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"order": order})
}

// SetProviderOrder godoc
//
// @Summary     Set provider priority order (admin)
// @Description Saves a provider order. Kept for older clients; lookups no longer use it, each field is pre-selected from the answers.
// @Tags        admin,providers
// @Accept      json
// @Security    BearerAuth
// @Param       body  body      object{order=[]string}  true  "Ordered provider names"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Router      /admin/providers/order [put]
func (h *ProviderHandler) SetProviderOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Order []string `json:"order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Order) == 0 {
		respond.Error(w, http.StatusBadRequest, "order array is required")
		return
	}
	if err := h.svc.SetProviderOrder(r.Context(), body.Order); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SearchBooks godoc
//
// @Summary     Search books across providers
// @Description Queries all enabled providers for books matching the freetext query.
// @Tags        lookup
// @Produce     json
// @Security    BearerAuth
// @Param       q    query     string  true  "Search query"
// @Success     200  {array}   providers.BookResult
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /lookup/books [get]
func (h *ProviderHandler) SearchBooks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		respond.Error(w, http.StatusBadRequest, "q (query) parameter is required")
		return
	}

	slog.InfoContext(r.Context(), "SearchBooks handler entered", "query", q)
	start := time.Now()

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	results := h.svc.SearchBooks(ctx, q)
	if results == nil {
		results = []*providers.BookResult{}
	}

	slog.InfoContext(r.Context(), "SearchBooks handler done",
		"query", q,
		"results", len(results),
		"elapsed", time.Since(start).String(),
		"ctx_err", ctx.Err(),
	)
	respond.JSON(w, http.StatusOK, results)
}

// SearchSeries godoc
//
// @Summary     Search series across providers
// @Description Queries enabled providers for series matching the search term.
// @Tags        lookup
// @Produce     json
// @Security    BearerAuth
// @Param       q    query     string  true  "Search query"
// @Success     200  {array}   providers.SeriesResult
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /lookup/series [get]
func (h *ProviderHandler) SearchSeries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		respond.Error(w, http.StatusBadRequest, "q (query) parameter is required")
		return
	}

	results := h.svc.SearchSeries(r.Context(), q)
	if results == nil {
		results = []providers.SeriesResult{}
	}
	respond.JSON(w, http.StatusOK, results)
}
