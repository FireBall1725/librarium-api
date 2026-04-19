// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"errors"
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

type EnrichmentBatchHandler struct {
	batches *repository.EnrichmentBatchRepo
}

func NewEnrichmentBatchHandler(batches *repository.EnrichmentBatchRepo) *EnrichmentBatchHandler {
	return &EnrichmentBatchHandler{batches: batches}
}

// ListAll godoc
//
// @Summary     List enrichment batches
// @Description Returns all enrichment batches for the calling user across all libraries.
// @Tags        enrichment
// @Produce     json
// @Security    BearerAuth
// @Success     200  {array}   models.EnrichmentBatch
// @Failure     401  {object}  object{error=string}
// @Router      /enrichment-batches [get]
func (h *EnrichmentBatchHandler) ListAll(w http.ResponseWriter, r *http.Request) {
	caller := middleware.ClaimsFromContext(r.Context())
	if caller == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	batches, err := h.batches.ListByUser(r.Context(), caller.UserID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	if batches == nil {
		batches = []models.EnrichmentBatch{}
	}
	respond.JSON(w, http.StatusOK, batches)
}

// Get godoc
//
// @Summary     Get an enrichment batch
// @Description Returns a specific enrichment batch with its per-book items.
// @Tags        enrichment
// @Produce     json
// @Security    BearerAuth
// @Param       batch_id  path      string  true  "Batch UUID"
// @Success     200  {object}  models.EnrichmentBatch
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /enrichment-batches/{batch_id} [get]
func (h *EnrichmentBatchHandler) Get(w http.ResponseWriter, r *http.Request) {
	caller := middleware.ClaimsFromContext(r.Context())
	if caller == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	batchID, err := uuid.Parse(r.PathValue("batch_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid batch id")
		return
	}

	batch, err := h.batches.GetWithItems(r.Context(), batchID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "batch not found")
			return
		}
		respond.ServerError(w, r, err)
		return
	}

	// Only let the creator see the batch.
	if batch.CreatedBy != caller.UserID {
		respond.Error(w, http.StatusNotFound, "batch not found")
		return
	}

	respond.JSON(w, http.StatusOK, batch)
}

// Cancel godoc
//
// @Summary     Cancel an enrichment batch
// @Description Requests cancellation of a running enrichment batch.
// @Tags        enrichment
// @Security    BearerAuth
// @Param       batch_id  path  string  true  "Batch UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /enrichment-batches/{batch_id}/cancel [post]
func (h *EnrichmentBatchHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	caller := middleware.ClaimsFromContext(r.Context())
	if caller == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	batchID, err := uuid.Parse(r.PathValue("batch_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid batch id")
		return
	}

	if err := h.batches.Cancel(r.Context(), batchID, caller.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "batch not found or cannot be cancelled")
			return
		}
		respond.ServerError(w, r, err)
		return
	}

	respond.JSON(w, http.StatusNoContent, nil)
}

// Delete godoc
//
// @Summary     Delete an enrichment batch
// @Description Deletes a finished enrichment batch record.
// @Tags        enrichment
// @Security    BearerAuth
// @Param       batch_id  path  string  true  "Batch UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /enrichment-batches/{batch_id} [delete]
func (h *EnrichmentBatchHandler) Delete(w http.ResponseWriter, r *http.Request) {
	caller := middleware.ClaimsFromContext(r.Context())
	if caller == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	batchID, err := uuid.Parse(r.PathValue("batch_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid batch id")
		return
	}

	if err := h.batches.Delete(r.Context(), batchID, caller.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "batch not found")
			return
		}
		respond.ServerError(w, r, err)
		return
	}

	respond.JSON(w, http.StatusNoContent, nil)
}

// DeleteFinished godoc
//
// @Summary     Delete all finished enrichment batches
// @Description Bulk-deletes all done/failed/cancelled enrichment batches for the calling user.
// @Tags        enrichment
// @Security    BearerAuth
// @Success     204
// @Failure     401  {object}  object{error=string}
// @Router      /enrichment-batches [delete]
func (h *EnrichmentBatchHandler) DeleteFinished(w http.ResponseWriter, r *http.Request) {
	caller := middleware.ClaimsFromContext(r.Context())
	if caller == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if err := h.batches.DeleteFinished(r.Context(), caller.UserID); err != nil {
		respond.ServerError(w, r, err)
		return
	}

	respond.JSON(w, http.StatusNoContent, nil)
}
