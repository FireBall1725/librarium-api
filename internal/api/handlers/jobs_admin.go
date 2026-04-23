// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/jobs"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

// UnifiedJobsHandler backs the /admin/jobs/history surface — one entry
// point that returns every kind of job in one shape. Kind-specific
// endpoints (the per-type routes under /admin/jobs/ai-suggestions/...,
// /enrichment-batches/..., /imports/...) stay for now so the existing
// Jobs page keeps working until the web PR lands on these unified
// endpoints.
type UnifiedJobsHandler struct {
	jobs     *repository.JobRepo
	registry *jobs.Registry
}

func NewUnifiedJobsHandler(jr *repository.JobRepo, registry *jobs.Registry) *UnifiedJobsHandler {
	return &UnifiedJobsHandler{jobs: jr, registry: registry}
}

// JobView is the wire-shape for one unified job row. Progress is a
// per-kind JSON blob already stored on the umbrella row; the web renders
// it via a kind-aware sub-component.
type JobView struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Status      string          `json:"status"`
	TriggeredBy string          `json:"triggered_by"`
	CreatedBy   *string         `json:"created_by,omitempty"`
	ScheduleID  *string         `json:"schedule_id,omitempty"`
	Error       string          `json:"error,omitempty"`
	Progress    json.RawMessage `json:"progress"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func toJobView(j *models.Job) JobView {
	v := JobView{
		ID:          j.ID.String(),
		Kind:        j.Kind,
		Status:      string(j.Status),
		TriggeredBy: string(j.TriggeredBy),
		Error:       j.Error,
		Progress:    j.Progress,
		StartedAt:   j.StartedAt,
		FinishedAt:  j.FinishedAt,
		CreatedAt:   j.CreatedAt,
		UpdatedAt:   j.UpdatedAt,
	}
	if j.CreatedBy != nil {
		s := j.CreatedBy.String()
		v.CreatedBy = &s
	}
	if j.ScheduleID != nil {
		s := j.ScheduleID.String()
		v.ScheduleID = &s
	}
	if len(v.Progress) == 0 {
		v.Progress = json.RawMessage("{}")
	}
	return v
}

// History godoc
//
//	@Summary     List unified job history
//	@Description Returns jobs across every kind in a single paginated list.
//	@Tags        admin,jobs
//	@Produce     json
//	@Security    BearerAuth
//	@Param       kind    query  string  false  "filter by kind"
//	@Param       status  query  string  false  "filter by status"
//	@Param       since   query  string  false  "RFC3339 or NNd"
//	@Param       limit   query  int     false  "default 50, max 500"
//	@Param       offset  query  int     false  "0-based"
//	@Success     200  {object}  object{items=[]handlers.JobView,total=int}
//	@Router      /admin/jobs/history [get]
func (h *UnifiedJobsHandler) History(w http.ResponseWriter, r *http.Request) {
	opts := repository.ListJobsOpts{
		Kind:   r.URL.Query().Get("kind"),
		Status: r.URL.Query().Get("status"),
	}
	if s := r.URL.Query().Get("since"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			opts.Since = &t
		} else if n, ok := parseJobDays(s); ok {
			t := time.Now().Add(-time.Duration(n) * 24 * time.Hour)
			opts.Since = &t
		} else {
			respond.Error(w, http.StatusBadRequest, "invalid since")
			return
		}
	}
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			opts.Limit = n
		}
	}
	if s := r.URL.Query().Get("offset"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			opts.Offset = n
		}
	}

	items, total, err := h.jobs.ListJobs(r.Context(), opts)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]JobView, 0, len(items))
	for _, j := range items {
		out = append(out, toJobView(j))
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": out, "total": total})
}

// Detail godoc
//
//	@Summary     Get unified job detail with event log
//	@Tags        admin,jobs
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path  string  true  "job id"
//	@Success     200  {object}  object{job=handlers.JobView,events=array}
//	@Router      /admin/jobs/{id} [get]
func (h *UnifiedJobsHandler) Detail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	j, err := h.jobs.GetJob(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	evts, err := h.jobs.ListEvents(r.Context(), id)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"job":    toJobView(j),
		"events": evts,
	})
}

// Delete godoc
//
//	@Summary     Delete a job entirely
//	@Tags        admin,jobs
//	@Security    BearerAuth
//	@Param       id   path  string  true  "job id"
//	@Success     204
//	@Router      /admin/jobs/{id} [delete]
func (h *UnifiedJobsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.jobs.DeleteJob(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "job not found")
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteHistory godoc
//
//	@Summary     Clear finished job history
//	@Tags        admin,jobs
//	@Produce     json
//	@Security    BearerAuth
//	@Param       kind  query  string  false  "restrict to one kind"
//	@Success     200  {object}  object{deleted=int}
//	@Router      /admin/jobs/history [delete]
func (h *UnifiedJobsHandler) DeleteHistory(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	n, err := h.jobs.DeleteFinished(r.Context(), kind)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// ─── Schedules ───────────────────────────────────────────────────────────────

// ScheduleView is a job_schedules row enriched with registry display info.
type ScheduleView struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	DisplayName string          `json:"display_name"`
	Description string          `json:"description"`
	Cron        string          `json:"cron"`
	Enabled     bool            `json:"enabled"`
	Config      json.RawMessage `json:"config"`
	LastFiredAt *time.Time      `json:"last_fired_at,omitempty"`
}

// ListSchedules godoc
//
//	@Summary     List job schedules
//	@Tags        admin,jobs
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {array}  handlers.ScheduleView
//	@Router      /admin/jobs/schedules [get]
func (h *UnifiedJobsHandler) ListSchedules(w http.ResponseWriter, r *http.Request) {
	rows, err := h.jobs.ListSchedules(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]ScheduleView, 0, len(rows))
	for _, s := range rows {
		v := ScheduleView{
			ID:          s.ID.String(),
			Kind:        s.Kind,
			Cron:        s.Cron,
			Enabled:     s.Enabled,
			Config:      s.Config,
			LastFiredAt: s.LastFiredAt,
		}
		if def := h.registry.Get(jobs.Kind(s.Kind)); def != nil {
			v.DisplayName = def.DisplayName
			v.Description = def.Description
		} else {
			v.DisplayName = s.Kind
		}
		if len(v.Config) == 0 {
			v.Config = json.RawMessage("{}")
		}
		out = append(out, v)
	}
	respond.JSON(w, http.StatusOK, out)
}

// UpdateSchedule godoc
//
//	@Summary     Upsert a job schedule
//	@Tags        admin,jobs
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       kind  path  string  true  "job kind"
//	@Param       body  body  object{cron=string,enabled=boolean,config=object}  true  "schedule"
//	@Success     200  {object}  handlers.ScheduleView
//	@Router      /admin/jobs/schedules/{kind} [put]
func (h *UnifiedJobsHandler) UpdateSchedule(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if kind == "" {
		respond.Error(w, http.StatusBadRequest, "missing kind")
		return
	}
	if h.registry.Get(jobs.Kind(kind)) == nil {
		respond.Error(w, http.StatusBadRequest, "unknown job kind")
		return
	}
	var body struct {
		Cron    string          `json:"cron"`
		Enabled bool            `json:"enabled"`
		Config  json.RawMessage `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Cron == "" {
		respond.Error(w, http.StatusBadRequest, "cron is required")
		return
	}
	sched := &models.JobSchedule{
		Kind:    kind,
		Cron:    body.Cron,
		Enabled: body.Enabled,
		Config:  body.Config,
	}
	if err := h.jobs.UpsertSchedule(r.Context(), sched); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	def := h.registry.Get(jobs.Kind(kind))
	respond.JSON(w, http.StatusOK, ScheduleView{
		ID:          sched.ID.String(),
		Kind:        sched.Kind,
		DisplayName: def.DisplayName,
		Description: def.Description,
		Cron:        sched.Cron,
		Enabled:     sched.Enabled,
		Config:      sched.Config,
	})
}

// parseJobDays accepts tokens like "30d", "7d" and returns the integer
// day count. Returns (0, false) for anything else.
func parseJobDays(s string) (int, bool) {
	if len(s) < 2 || s[len(s)-1] != 'd' {
		return 0, false
	}
	n := 0
	for _, r := range s[:len(s)-1] {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	if n <= 0 || n > 3650 {
		return 0, false
	}
	return n, true
}
