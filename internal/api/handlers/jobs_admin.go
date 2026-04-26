// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/jobs"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// cronParser parses the standard 5-field expressions the UI emits. Kept
// package-level so repeated calls don't reallocate; Parse itself is
// stateless so this is safe to reuse.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// UnifiedJobsHandler backs the /admin/jobs/history surface — one entry
// point that returns every kind of job in one shape. Kind-specific
// endpoints (the per-type routes under /admin/jobs/ai-suggestions/...,
// /enrichment-batches/..., /imports/...) stay for now so the existing
// Jobs page keeps working until the web PR lands on these unified
// endpoints.
type UnifiedJobsHandler struct {
	jobs        *repository.JobRepo
	registry    *jobs.Registry
	importJobs  *repository.ImportJobRepo
	enrichments *repository.EnrichmentBatchRepo
}

func NewUnifiedJobsHandler(jr *repository.JobRepo, registry *jobs.Registry, importJobs *repository.ImportJobRepo, enrichments *repository.EnrichmentBatchRepo) *UnifiedJobsHandler {
	return &UnifiedJobsHandler{jobs: jr, registry: registry, importJobs: importJobs, enrichments: enrichments}
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
	// KindID is the per-kind detail row's primary key when one exists
	// (import_jobs.id, enrichment_batches.id). Clients use it to deep-link
	// the umbrella row back to its detail endpoint, which is keyed by the
	// per-kind id rather than the umbrella job id. Empty for kinds that
	// have no detail table (cover_backfill, ai_suggestions today).
	KindID      *string `json:"kind_id,omitempty"`
	LibraryID   *string `json:"library_id,omitempty"`
	LibraryName *string `json:"library_name,omitempty"`
	// Subtype is the per-kind discriminator surfaced for UI badges.
	// Today only enrichment uses it ("metadata" or "cover"); other
	// kinds leave it empty and the client falls back to Kind alone.
	Subtype *string `json:"subtype,omitempty"`
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
		Kind:    r.URL.Query().Get("kind"),
		Subtype: r.URL.Query().Get("subtype"),
		Status:  r.URL.Query().Get("status"),
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

	// Collect umbrella ids per kind so the per-kind detail tables can be
	// resolved in a single query each. The unified row carries no detail
	// pointer of its own — the web needs (kind_id, library_id) to call
	// the existing per-kind GET endpoints, and without these the items
	// list silently renders as "No items".
	var importIDs, enrichIDs []uuid.UUID
	for _, j := range items {
		switch j.Kind {
		case "import":
			importIDs = append(importIDs, j.ID)
		case "enrichment":
			enrichIDs = append(enrichIDs, j.ID)
		}
	}
	importRefs, err := h.importJobs.LookupByJobIDs(r.Context(), importIDs)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	enrichRefs, err := h.enrichments.LookupByJobIDs(r.Context(), enrichIDs)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	out := make([]JobView, 0, len(items))
	for _, j := range items {
		v := toJobView(j)
		var ref repository.JobRef
		var ok bool
		switch j.Kind {
		case "import":
			ref, ok = importRefs[j.ID]
		case "enrichment":
			ref, ok = enrichRefs[j.ID]
		}
		if ok {
			id := ref.ID.String()
			v.KindID = &id
			if ref.LibraryID != (uuid.UUID{}) {
				lid := ref.LibraryID.String()
				v.LibraryID = &lid
			}
			if ref.LibraryName != "" {
				name := ref.LibraryName
				v.LibraryName = &name
			}
			if ref.Subtype != "" {
				st := ref.Subtype
				v.Subtype = &st
			}
		}
		out = append(out, v)
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
// next_fire_at is computed server-side from the cron expression + the
// schedule row's last_fired_at (or created_at when never fired). The UI
// uses it to render a live countdown and sort; keeping the calculation
// server-side avoids cross-timezone weirdness on the client.
type ScheduleView struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	DisplayName string          `json:"display_name"`
	Description string          `json:"description"`
	Cron        string          `json:"cron"`
	Enabled     bool            `json:"enabled"`
	Config      json.RawMessage `json:"config"`
	LastFiredAt *time.Time      `json:"last_fired_at,omitempty"`
	NextFireAt  *time.Time      `json:"next_fire_at,omitempty"`
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
		// Compute next fire only for enabled schedules — a disabled row
		// has no meaningful "next run" and the UI can render a dash.
		if s.Enabled {
			if t, ok := computeNextFire(s); ok {
				v.NextFireAt = &t
			}
		}
		out = append(out, v)
	}
	respond.JSON(w, http.StatusOK, out)
}

// computeNextFire parses the schedule's cron expression and returns the
// next scheduled fire time based on last_fired_at (or created_at when
// the schedule has never fired). Mirrors the scheduler's own logic so
// the UI sees the same next-run the scheduler will actually use.
func computeNextFire(s *models.JobSchedule) (time.Time, bool) {
	sched, err := cronParser.Parse(s.Cron)
	if err != nil {
		return time.Time{}, false
	}
	prev := s.CreatedAt
	if s.LastFiredAt != nil {
		prev = *s.LastFiredAt
	}
	next := sched.Next(prev)
	// If the computed next is already in the past (admin re-enabled a
	// schedule whose last fire was long ago), roll forward to the next
	// fire from "now".
	if next.Before(time.Now()) {
		next = sched.Next(time.Now())
	}
	return next, true
}

// RunNow godoc
//
//	@Summary     Run a scheduled job once, now
//	@Description Fires the kind's Enqueue hook immediately, bypassing the
//	@Description cron. Creates an umbrella jobs row with triggered_by=admin
//	@Description so the run shows up in history as admin-triggered.
//	@Tags        admin,jobs
//	@Security    BearerAuth
//	@Param       kind  path  string  true  "job kind"
//	@Success     202   {object}  handlers.JobView
//	@Failure     400   {object}  object{error=string}
//	@Failure     404   {object}  object{error=string}
//	@Router      /admin/jobs/schedules/{kind}/run [post]
func (h *UnifiedJobsHandler) RunNow(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if kind == "" {
		respond.Error(w, http.StatusBadRequest, "missing kind")
		return
	}
	def := h.registry.Get(jobs.Kind(kind))
	if def == nil {
		respond.Error(w, http.StatusNotFound, "unknown job kind")
		return
	}
	if def.Enqueue == nil {
		respond.Error(w, http.StatusBadRequest, "this kind doesn't support manual run")
		return
	}
	// Look up the schedule's config (if any) — admin-run uses whatever
	// the kind has stored. Missing schedule = empty config.
	cfg := json.RawMessage("{}")
	if s, err := h.jobs.GetSchedule(r.Context(), kind); err == nil && s != nil {
		if len(s.Config) > 0 {
			cfg = s.Config
		}
	}

	now := time.Now()
	j := &models.Job{
		Kind:        kind,
		Status:      models.JobStatusRunning,
		TriggeredBy: models.JobTriggeredByAdmin,
		StartedAt:   &now,
	}
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		uid := claims.UserID
		j.CreatedBy = &uid
	}
	if err := h.jobs.CreateJob(r.Context(), j); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	if err := def.Enqueue(r.Context(), jobs.TriggerCtx{
		JobID:       j.ID,
		TriggeredBy: models.JobTriggeredByAdmin,
		CreatedBy:   j.CreatedBy,
	}, cfg); err != nil {
		_ = h.jobs.MarkFinished(r.Context(), j.ID, models.JobStatusFailed, err.Error())
		respond.ServerError(w, r, err)
		return
	}
	_ = h.jobs.MarkFinished(r.Context(), j.ID, models.JobStatusCompleted, "")
	respond.JSON(w, http.StatusAccepted, toJobView(j))
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
