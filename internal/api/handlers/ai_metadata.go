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
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/google/uuid"
)

// extendWriteDeadlineForAI lifts the response write deadline past the server's
// default 15s WriteTimeout. AI calls (Anthropic Opus on a complex prompt) can
// take 60–90 seconds; without this the connection gets torn down mid-handler
// and the client sees a 502 even though the server eventually writes a 201.
// The handler keeps running because Go doesn't cancel the goroutine on a
// dropped connection — that's why the proposal still lands in the DB.
func extendWriteDeadlineForAI(w http.ResponseWriter) {
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Minute))
	}
}

// AIMetadataHandler exposes the AI-assisted metadata enrichment surfaces:
// kicking off a suggestion run, listing pending proposals, accepting (with
// optional partial selection), and rejecting.
type AIMetadataHandler struct {
	aiMeta    *service.AIMetadataService
	series    *repository.SeriesRepo
	arcs      *repository.SeriesArcRepo
	proposals *repository.AIMetadataRepo
}

func NewAIMetadataHandler(aiMeta *service.AIMetadataService, series *repository.SeriesRepo, arcs *repository.SeriesArcRepo, proposals *repository.AIMetadataRepo) *AIMetadataHandler {
	return &AIMetadataHandler{aiMeta: aiMeta, series: series, arcs: arcs, proposals: proposals}
}

// SuggestSeriesArcs godoc
//
// @Summary     Generate an AI suggestion for a series's arc list
// @Description Synchronously calls the active AI provider with the series name + volume count and writes a pending proposal that the user can review on the series detail page. Records the prompt + response in `ai_metadata_runs` for inspection.
// @Tags        series,ai
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       series_id   path      string  true  "Series UUID"
// @Success     201  {object}  object{proposal_id=string}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     503  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id}/suggest-arcs [post]
func (h *AIMetadataHandler) SuggestSeriesArcs(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadlineForAI(w)
	libraryID, seriesID, ok := h.parseLibSeries(w, r)
	if !ok {
		return
	}
	caller := middleware.ClaimsFromContext(r.Context())
	if caller == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ser, err := h.series.FindByID(r.Context(), seriesID, uuid.Nil)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "series not found")
			return
		}
		respond.ServerError(w, r, err)
		return
	}

	proposalID, jobID, err := h.aiMeta.SuggestSeriesArcs(r.Context(), service.AICallContext{
		LibraryID:   &libraryID,
		TriggeredBy: &caller.UserID,
	}, libraryID, seriesID, ser.Name, ser.TotalCount)
	if err != nil {
		respondAIError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, map[string]any{"proposal_id": proposalID, "job_id": jobID})
}

// SuggestSeriesMetadata godoc
//
// @Summary     Generate an AI suggestion for series metadata fields
// @Description Synchronously calls the active AI provider to propose status, total_count, demographic, genres, and a cleaned description for a series. Writes a pending proposal that the user reviews per-field on the series detail page.
// @Tags        series,ai
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       series_id   path      string  true  "Series UUID"
// @Success     201  {object}  object{proposal_id=string}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     503  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id}/suggest-metadata [post]
func (h *AIMetadataHandler) SuggestSeriesMetadata(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadlineForAI(w)
	libraryID, seriesID, ok := h.parseLibSeries(w, r)
	if !ok {
		return
	}
	caller := middleware.ClaimsFromContext(r.Context())
	if caller == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ser, err := h.series.FindByID(r.Context(), seriesID, uuid.Nil)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "series not found")
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	proposalID, jobID, err := h.aiMeta.SuggestSeriesMetadata(r.Context(), service.AICallContext{
		LibraryID:   &libraryID,
		TriggeredBy: &caller.UserID,
	}, libraryID, seriesID, ser.Name, ser.Description, ser.TotalCount)
	if err != nil {
		respondAIError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, map[string]any{"proposal_id": proposalID, "job_id": jobID})
}

// respondAIError surfaces upstream AI provider errors to the client with
// enough detail that the user can fix the cause (e.g. "401 invalid x-api-key"
// pointing at a stale Anthropic key). Generic "internal server error" hides
// these and makes the AI suggest buttons feel broken when really the config
// is wrong. The raw error is also logged for the admin via slog.
func respondAIError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, service.ErrNoActiveAIProvider) {
		respond.Error(w, http.StatusServiceUnavailable, "no active AI provider configured")
		return
	}
	respond.SetHandlerError(r.Context(), err)
	respond.Error(w, http.StatusBadGateway, "AI provider error: "+err.Error())
}

// ListSeriesProposals godoc
//
// @Summary     List AI suggestion proposals for a series
// @Description Returns proposals (pending, accepted, rejected) for the target series, newest first.
// @Tags        series,ai
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       series_id   path      string  true  "Series UUID"
// @Param       status      query     string  false  "Filter by status (pending|accepted|rejected|partially_accepted)"
// @Success     200  {array}   models.AIMetadataProposal
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/series/{series_id}/proposals [get]
func (h *AIMetadataHandler) ListSeriesProposals(w http.ResponseWriter, r *http.Request) {
	_, seriesID, ok := h.parseLibSeries(w, r)
	if !ok {
		return
	}
	statusFilter := r.URL.Query().Get("status")
	out, err := h.proposals.ListProposals(r.Context(), models.AIMetaTargetSeries, seriesID, statusFilter)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	if out == nil {
		out = []*models.AIMetadataProposal{}
	}
	respond.JSON(w, http.StatusOK, out)
}

// acceptRequest lets the caller selectively accept fields / arcs from a
// proposal. Empty fields/indices = "accept everything".
type acceptRequest struct {
	// For series_metadata proposals — names of fields to apply. Empty slice
	// means "apply every populated field".
	Fields []string `json:"fields,omitempty"`
	// For series_arcs proposals — zero-based indices into the proposed arcs
	// array. Empty slice means "accept every proposed arc".
	ArcIndices []int `json:"arc_indices,omitempty"`
	// AssignBooks: when accepting arcs, also assign existing series books
	// whose position falls within each arc's vol_start / vol_end range.
	// Defaults to true when omitted (server-side via *bool).
	AssignBooks *bool `json:"assign_books,omitempty"`
}

// AcceptProposal godoc
//
// @Summary     Apply an AI suggestion proposal
// @Description Writes accepted fields / arcs onto the target series. Body lets the user partially accept (`fields` / `arc_indices`); empty body accepts everything. Arc proposals are destructive — accepting deletes every existing arc on the series and replaces it with the proposed arcs (book→arc assignments are cleared via ON DELETE SET NULL and re-derived from each new arc's volume range, unless `assign_books=false`).
// @Tags        series,ai
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id   path  string  true  "Library UUID"
// @Param       proposal_id  path  string  true  "Proposal UUID"
// @Param       body         body  acceptRequest  false  "Selective acceptance"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/proposals/{proposal_id}/accept [post]
func (h *AIMetadataHandler) AcceptProposal(w http.ResponseWriter, r *http.Request) {
	proposalID, err := uuid.Parse(r.PathValue("proposal_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid proposal id")
		return
	}
	caller := middleware.ClaimsFromContext(r.Context())
	if caller == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	prop, err := h.proposals.GetProposal(r.Context(), proposalID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "proposal not found")
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	if prop.Status != models.AIMetaProposalStatusPending {
		respond.Error(w, http.StatusBadRequest, "proposal is no longer pending")
		return
	}

	var req acceptRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	switch prop.Kind {
	case models.AIMetaKindSeriesMetadata:
		if err := h.applySeriesMetadata(r, prop, req); err != nil {
			respond.ServerError(w, r, err)
			return
		}
	case models.AIMetaKindSeriesArcs:
		if err := h.applySeriesArcs(r, prop, req); err != nil {
			respond.ServerError(w, r, err)
			return
		}
	default:
		respond.Error(w, http.StatusBadRequest, "unknown proposal kind")
		return
	}

	fullAccept := len(req.Fields) == 0 && len(req.ArcIndices) == 0
	if err := h.proposals.MarkProposalApplied(r.Context(), proposalID, caller.UserID, fullAccept); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// JobRunDetail godoc
//
// @Summary     Get an AI metadata run as a {run, events} timeline
// @Description Same shape as the AI suggestions run-detail endpoint so the
// @Description shared `RunDetailPanel` component can render either kind.
// @Description Synthesises a 2-event timeline (prompt + ai_response) from the
// @Description prompt + response_text columns on `ai_metadata_runs`.
// @Tags        jobs,ai
// @Produce     json
// @Security    BearerAuth
// @Param       job_id  path      string  true  "Job UUID"
// @Success     200  {object}  object{run=object,events=array}
// @Failure     400  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /admin/jobs/ai-metadata/runs/{job_id} [get]
func (h *AIMetadataHandler) JobRunDetail(w http.ResponseWriter, r *http.Request) {
	jobID, err := uuid.Parse(r.PathValue("job_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid job id")
		return
	}
	runs, err := h.proposals.ListRunsForJob(r.Context(), jobID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	if len(runs) == 0 {
		respond.Error(w, http.StatusNotFound, "no AI run for this job")
		return
	}
	// Per-job AI metadata calls are 1:1 with the job today (sync suggest-*).
	// If we ever batch multiple in one job we'll fold them all into a single
	// timeline ordered by started_at.
	first := runs[0]

	type runView struct {
		ID               string  `json:"id"`
		TriggeredBy      string  `json:"triggered_by"`
		ProviderType     string  `json:"provider_type"`
		ModelID          string  `json:"model_id,omitempty"`
		Status           string  `json:"status"`
		Error            string  `json:"error,omitempty"`
		TokensIn         int     `json:"tokens_in"`
		TokensOut        int     `json:"tokens_out"`
		EstimatedCostUSD float64 `json:"estimated_cost_usd"`
		StartedAt        string  `json:"started_at"`
		FinishedAt       string  `json:"finished_at,omitempty"`
	}
	type eventView struct {
		Seq       int            `json:"seq"`
		Type      string         `json:"type"`
		Content   map[string]any `json:"content"`
		CreatedAt string         `json:"created_at"`
	}

	rv := runView{
		ID:               first.ID.String(),
		TriggeredBy:      "user",
		ProviderType:     first.ProviderType,
		ModelID:          first.ModelID,
		Status:           first.Status,
		Error:            first.Error,
		TokensIn:         first.TokensIn,
		TokensOut:        first.TokensOut,
		EstimatedCostUSD: first.EstimatedCostUSD,
		StartedAt:        first.StartedAt.Format("2006-01-02T15:04:05.999999Z07:00"),
	}
	if first.FinishedAt != nil {
		rv.FinishedAt = first.FinishedAt.Format("2006-01-02T15:04:05.999999Z07:00")
	}

	events := []eventView{}
	seq := 1
	for _, run := range runs {
		// The umbrella `pipeline_start` flag gives the timeline a recognisable
		// opener so it reads consistently with ai_suggestion runs.
		events = append(events, eventView{
			Seq:  seq,
			Type: "pipeline_start",
			Content: map[string]any{
				"kind":   run.Kind,
				"target": run.TargetType + " " + run.TargetID.String(),
				"model":  run.ModelID,
			},
			CreatedAt: run.StartedAt.Format("2006-01-02T15:04:05.999999Z07:00"),
		})
		seq++
		events = append(events, eventView{
			Seq:       seq,
			Type:      "prompt",
			Content:   map[string]any{"prompt": run.Prompt, "model": run.ModelID},
			CreatedAt: run.StartedAt.Format("2006-01-02T15:04:05.999999Z07:00"),
		})
		seq++
		when := run.StartedAt
		if run.FinishedAt != nil {
			when = *run.FinishedAt
		}
		if run.Status == models.AIMetaRunStatusFailed && run.Error != "" {
			events = append(events, eventView{
				Seq:       seq,
				Type:      "error",
				Content:   map[string]any{"error": run.Error},
				CreatedAt: when.Format("2006-01-02T15:04:05.999999Z07:00"),
			})
			seq++
		} else {
			events = append(events, eventView{
				Seq:  seq,
				Type: "ai_response",
				Content: map[string]any{
					"text":       run.ResponseText,
					"model":      run.ModelID,
					"tokens_in":  run.TokensIn,
					"tokens_out": run.TokensOut,
				},
				CreatedAt: when.Format("2006-01-02T15:04:05.999999Z07:00"),
			})
			seq++
		}
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"run":    rv,
		"events": events,
	})
}

// RejectProposal godoc
//
// @Summary     Reject an AI suggestion proposal
// @Tags        series,ai
// @Security    BearerAuth
// @Param       library_id   path  string  true  "Library UUID"
// @Param       proposal_id  path  string  true  "Proposal UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/proposals/{proposal_id}/reject [post]
func (h *AIMetadataHandler) RejectProposal(w http.ResponseWriter, r *http.Request) {
	proposalID, err := uuid.Parse(r.PathValue("proposal_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid proposal id")
		return
	}
	if err := h.proposals.MarkProposalRejected(r.Context(), proposalID); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (h *AIMetadataHandler) parseLibSeries(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return uuid.Nil, uuid.Nil, false
	}
	seriesID, err := uuid.Parse(r.PathValue("series_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid series id")
		return uuid.Nil, uuid.Nil, false
	}
	return libraryID, seriesID, true
}

func (h *AIMetadataHandler) applySeriesMetadata(r *http.Request, prop *models.AIMetadataProposal, req acceptRequest) error {
	var p models.SeriesMetadataPayload
	if err := json.Unmarshal(prop.Payload, &p); err != nil {
		return err
	}
	ser, err := h.series.FindByID(r.Context(), prop.TargetID, uuid.Nil)
	if err != nil {
		return err
	}
	// Build a set of fields to apply. Empty req.Fields = apply everything
	// the AI populated.
	wanted := map[string]bool{}
	allFields := req.Fields
	if len(allFields) == 0 {
		allFields = []string{"status", "total_count", "demographic", "genres", "description"}
	}
	for _, f := range allFields {
		wanted[f] = true
	}

	updates := struct {
		Name             string
		Description      string
		TotalCount       *int
		Status           string
		OriginalLanguage string
		PublicationYear  *int
		Demographic      string
		Genres           []string
		URL              string
		ExternalID       string
		ExternalSource   string
	}{
		Name:             ser.Name,
		Description:      ser.Description,
		TotalCount:       ser.TotalCount,
		Status:           ser.Status,
		OriginalLanguage: ser.OriginalLanguage,
		PublicationYear:  ser.PublicationYear,
		Demographic:      ser.Demographic,
		Genres:           ser.Genres,
		URL:              ser.URL,
		ExternalID:       ser.ExternalID,
		ExternalSource:   ser.ExternalSource,
	}
	if wanted["status"] && p.Status != nil {
		updates.Status = *p.Status
	}
	if wanted["total_count"] && p.TotalCount != nil {
		v := *p.TotalCount
		updates.TotalCount = &v
	}
	if wanted["demographic"] && p.Demographic != nil {
		updates.Demographic = *p.Demographic
	}
	if wanted["genres"] && len(p.Genres) > 0 {
		updates.Genres = p.Genres
	}
	if wanted["description"] && p.Description != nil {
		updates.Description = *p.Description
	}

	_, err = h.series.Update(r.Context(), ser.ID, updates.Name, updates.Description, updates.TotalCount, updates.Status, updates.OriginalLanguage, updates.PublicationYear, updates.Demographic, updates.Genres, updates.URL, updates.ExternalID, updates.ExternalSource)
	return err
}

func (h *AIMetadataHandler) applySeriesArcs(r *http.Request, prop *models.AIMetadataProposal, req acceptRequest) error {
	var p models.SeriesArcsPayload
	if err := json.Unmarshal(prop.Payload, &p); err != nil {
		return err
	}
	if len(p.Arcs) == 0 {
		return nil
	}
	wanted := map[int]bool{}
	for _, idx := range req.ArcIndices {
		wanted[idx] = true
	}
	acceptAll := len(req.ArcIndices) == 0
	assignBooks := req.AssignBooks == nil || *req.AssignBooks

	// Destructive replace: delete every existing arc for this series first.
	// `book_series.arc_id` has ON DELETE SET NULL, so any prior book→arc
	// assignments are cleared automatically; we re-derive them below from
	// the new arcs' volume ranges. The UI confirms with the user before
	// firing this when arcs already exist.
	existing, err := h.arcs.List(r.Context(), prop.TargetID)
	if err != nil {
		return err
	}
	for _, a := range existing {
		if err := h.arcs.Delete(r.Context(), a.ID); err != nil {
			return err
		}
	}

	for i, arc := range p.Arcs {
		if !acceptAll && !wanted[i] {
			continue
		}
		var volStart, volEnd *float64
		if arc.VolStart != nil {
			v := float64(*arc.VolStart)
			volStart = &v
		}
		if arc.VolEnd != nil {
			v := float64(*arc.VolEnd)
			volEnd = &v
		}
		created, err := h.arcs.Create(r.Context(), uuid.New(), prop.TargetID, arc.Name, "", float64(arc.Position), volStart, volEnd)
		if err != nil {
			return err
		}
		if assignBooks && arc.VolStart != nil && arc.VolEnd != nil {
			if err := h.assignBooksInRange(r, prop.TargetID, created.ID, *arc.VolStart, *arc.VolEnd); err != nil {
				return err
			}
		}
	}
	return nil
}

// assignBooksInRange links every book in the series whose volume position is
// within [start, end] to the given arc, only when the book is currently
// unassigned. Won't override a manual assignment the user has already made.
func (h *AIMetadataHandler) assignBooksInRange(r *http.Request, seriesID, arcID uuid.UUID, start, end int) error {
	entries, err := h.series.ListBooks(r.Context(), seriesID, uuid.Nil)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.ArcID != nil {
			continue // don't override
		}
		pos := int(e.Position)
		if pos < start || pos > end {
			continue
		}
		if err := h.arcs.SetBookArc(r.Context(), seriesID, e.BookID, &arcID); err != nil {
			return err
		}
	}
	return nil
}
