// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/service"
)

// JobsHandler serves admin endpoints for configurable scheduled jobs. Today
// that's the AI suggestions job only; more drop in as the jobs framework grows.
type JobsHandler struct {
	svc *service.JobService
}

func NewJobsHandler(svc *service.JobService) *JobsHandler {
	return &JobsHandler{svc: svc}
}

// ListJobs godoc
//
//	@Summary     List configurable jobs (admin)
//	@Description Returns a summary of every admin-configurable scheduled job.
//	@Tags        admin,jobs
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {array}  service.JobSummary
//	@Router      /admin/jobs [get]
func (h *JobsHandler) ListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.svc.ListJobs(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, jobs)
}

// GetAISuggestionsJob godoc
//
//	@Summary     Get AI suggestions job config (admin)
//	@Tags        admin,jobs
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {object}  service.AISuggestionsJobConfig
//	@Router      /admin/jobs/ai-suggestions [get]
func (h *JobsHandler) GetAISuggestionsJob(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.svc.GetAISuggestionsConfig(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, cfg)
}

// UpdateAISuggestionsJob godoc
//
//	@Summary     Update AI suggestions job config (admin)
//	@Tags        admin,jobs
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body  body      service.AISuggestionsJobConfig  true  "Job config"
//	@Success     200   {object}  service.AISuggestionsJobConfig
//	@Failure     400   {object}  object{error=string}
//	@Router      /admin/jobs/ai-suggestions [put]
func (h *JobsHandler) UpdateAISuggestionsJob(w http.ResponseWriter, r *http.Request) {
	var cfg service.AISuggestionsJobConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.SetAISuggestionsConfig(r.Context(), cfg); err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, cfg)
}
