// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

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
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// AISuggestionsHandler groups user-facing suggestion endpoints: list, change
// status (dismiss / interested / added_to_library), block, and run-now.
type AISuggestionsHandler struct {
	repo        *repository.AISuggestionsRepo
	riverClient *river.Client[pgx.Tx]
	jobSvc      *service.JobService
}

func NewAISuggestionsHandler(repo *repository.AISuggestionsRepo, riverClient *river.Client[pgx.Tx], jobSvc *service.JobService) *AISuggestionsHandler {
	return &AISuggestionsHandler{repo: repo, riverClient: riverClient, jobSvc: jobSvc}
}

// SuggestionView is the JSON shape the UI consumes. Nullable pointer IDs are
// lifted into strings (or empty) so the client never deals with pointers.
type SuggestionView struct {
	ID            uuid.UUID `json:"id"`
	Type          string    `json:"type"`
	BookID        string    `json:"book_id,omitempty"`
	BookEditionID string    `json:"book_edition_id,omitempty"`
	LibraryID     string    `json:"library_id,omitempty"`
	Title         string    `json:"title"`
	Author        string    `json:"author,omitempty"`
	ISBN          string    `json:"isbn,omitempty"`
	CoverURL      string    `json:"cover_url,omitempty"`
	Reasoning     string    `json:"reasoning,omitempty"`
	Status        string    `json:"status"`
	CreatedAt     string    `json:"created_at"`
}

func toView(s *models.AISuggestionWithLibrary) SuggestionView {
	v := SuggestionView{
		ID:        s.ID,
		Type:      s.Type,
		Title:     s.Title,
		Author:    s.Author,
		ISBN:      s.ISBN,
		CoverURL:  s.CoverURL,
		Reasoning: s.Reasoning,
		Status:    s.Status,
		CreatedAt: s.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if s.BookID != nil {
		v.BookID = s.BookID.String()
	}
	if s.BookEditionID != nil {
		v.BookEditionID = s.BookEditionID.String()
	}
	if s.LibraryID != nil {
		v.LibraryID = s.LibraryID.String()
	}
	return v
}

// ListSuggestions godoc
//
//	@Summary     List my AI suggestions
//	@Description Returns the current AI-generated recommendations for the caller. Filter by type (buy | read_next) and status.
//	@Tags        me,ai
//	@Produce     json
//	@Security    BearerAuth
//	@Param       type    query     string  false  "Filter by type: buy | read_next"
//	@Param       status  query     string  false  "Filter by status: new | dismissed | interested | added_to_library"
//	@Success     200     {array}   handlers.SuggestionView
//	@Router      /me/suggestions [get]
func (h *AISuggestionsHandler) ListSuggestions(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	typeFilter := r.URL.Query().Get("type")
	statusFilter := r.URL.Query().Get("status")
	if statusFilter == "" {
		statusFilter = "new"
	}
	items, err := h.repo.ListSuggestions(r.Context(), claims.UserID, typeFilter, statusFilter)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]SuggestionView, 0, len(items))
	for _, it := range items {
		out = append(out, toView(it))
	}
	respond.JSON(w, http.StatusOK, out)
}

// UpdateSuggestionStatus godoc
//
//	@Summary     Update a suggestion's status
//	@Description Mark a suggestion as dismissed, interested, or added_to_library.
//	@Tags        me,ai
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id    path      string                    true  "Suggestion ID"
//	@Param       body  body      object{status=string}     true  "New status"
//	@Success     204
//	@Failure     400   {object}  object{error=string}
//	@Failure     404   {object}  object{error=string}
//	@Router      /me/suggestions/{id}/status [put]
func (h *AISuggestionsHandler) UpdateSuggestionStatus(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch body.Status {
	case "new", "dismissed", "interested", "added_to_library":
	default:
		respond.Error(w, http.StatusBadRequest, "invalid status value")
		return
	}
	if err := h.repo.UpdateSuggestionStatus(r.Context(), id, claims.UserID, body.Status); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "suggestion not found")
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// BlockSuggestion godoc
//
//	@Summary     Block a suggestion so it never reappears
//	@Description Creates a persistent block scoped to the book, the author, or the series.
//	@Tags        me,ai
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id    path      string                   true  "Suggestion ID"
//	@Param       body  body      object{scope=string}     true  "Block scope: book | author | series"
//	@Success     204
//	@Failure     400   {object}  object{error=string}
//	@Router      /me/suggestions/{id}/block [post]
func (h *AISuggestionsHandler) BlockSuggestion(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Scope string `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sugg, err := h.repo.GetSuggestion(r.Context(), id, claims.UserID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "suggestion not found")
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	blk := models.AIBlockedItem{UserID: claims.UserID, Scope: body.Scope}
	switch body.Scope {
	case "book":
		blk.Title = sugg.Title
		blk.Author = sugg.Author
		blk.ISBN = sugg.ISBN
	case "author":
		if sugg.Author == "" {
			respond.Error(w, http.StatusBadRequest, "cannot block by author: author unknown")
			return
		}
		blk.Author = sugg.Author
	case "series":
		// Series info isn't on the suggestion itself; the UI would need to pass
		// it explicitly. For v1 we reject series blocks from this endpoint and
		// leave it to a future richer block API.
		respond.Error(w, http.StatusBadRequest, "series blocks not yet supported from this endpoint")
		return
	default:
		respond.Error(w, http.StatusBadRequest, "invalid scope value")
		return
	}
	if err := h.repo.AddBlock(r.Context(), blk); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	// Also mark the suggestion dismissed so it drops out of the "new" view.
	_ = h.repo.UpdateSuggestionStatus(r.Context(), id, claims.UserID, "dismissed")
	w.WriteHeader(http.StatusNoContent)
}

// RunNow godoc
//
//	@Summary     Trigger a suggestions run for the caller
//	@Description Enqueues a background job to regenerate suggestions. Rate-limited by admin config.
//	@Tags        me,ai
//	@Produce     json
//	@Security    BearerAuth
//	@Success     202
//	@Failure     429  {object}  object{error=string}
//	@Router      /me/suggestions/run [post]
func (h *AISuggestionsHandler) RunNow(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	// Enforce the per-user rate limit up front so the user gets immediate
	// feedback instead of silently queued jobs that the worker would bounce.
	cfg, err := h.jobSvc.GetAISuggestionsConfig(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	if cfg.UserRunRateLimitPerDay > 0 {
		n, err := h.repo.RunsInLast24h(r.Context(), claims.UserID)
		if err != nil {
			respond.ServerError(w, r, err)
			return
		}
		if n >= cfg.UserRunRateLimitPerDay {
			respond.Error(w, http.StatusTooManyRequests, "daily suggestion run limit reached; try again tomorrow")
			return
		}
	}
	if _, err := h.riverClient.Insert(r.Context(),
		models.AISuggestionsJobArgs{UserID: claims.UserID, TriggeredBy: "user"}, nil); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// ─── Run observability ────────────────────────────────────────────────────────

// RunView is the JSON shape for a single suggestions run — shared by user and
// admin endpoints. UserID is only populated for admin-scoped responses.
type RunView struct {
	ID               uuid.UUID `json:"id"`
	UserID           string    `json:"user_id,omitempty"`
	TriggeredBy      string    `json:"triggered_by"`
	ProviderType     string    `json:"provider_type"`
	ModelID          string    `json:"model_id,omitempty"`
	Status           string    `json:"status"`
	Error            string    `json:"error,omitempty"`
	TokensIn         int       `json:"tokens_in"`
	TokensOut        int       `json:"tokens_out"`
	EstimatedCostUSD float64   `json:"estimated_cost_usd"`
	StartedAt        string    `json:"started_at"`
	FinishedAt       string    `json:"finished_at,omitempty"`
}

// EventView is the JSON shape for one pipeline event. Content is emitted as
// raw JSON so the UI can interpret each type however it likes.
type EventView struct {
	Seq       int             `json:"seq"`
	Type      string          `json:"type"`
	Content   json.RawMessage `json:"content"`
	CreatedAt string          `json:"created_at"`
}

func runToView(r *models.AISuggestionRun, includeUser bool) RunView {
	v := RunView{
		ID:               r.ID,
		TriggeredBy:      r.TriggeredBy,
		ProviderType:     r.ProviderType,
		ModelID:          r.ModelID,
		Status:           r.Status,
		Error:            r.Error,
		TokensIn:         r.TokensIn,
		TokensOut:        r.TokensOut,
		EstimatedCostUSD: r.EstimatedCostUSD,
		StartedAt:        r.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if includeUser {
		v.UserID = r.UserID.String()
	}
	if r.FinishedAt != nil {
		v.FinishedAt = r.FinishedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return v
}

// ListMyRuns godoc
//
//	@Summary     List my recent AI suggestion runs
//	@Description Returns the caller's most recent suggestion runs with cost and status. Newest first.
//	@Tags        me,ai
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {array}  handlers.RunView
//	@Router      /me/suggestions/runs [get]
func (h *AISuggestionsHandler) ListMyRuns(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	runs, err := h.repo.ListRunsByUser(r.Context(), claims.UserID, 25)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]RunView, 0, len(runs))
	for _, run := range runs {
		out = append(out, runToView(run, false))
	}
	respond.JSON(w, http.StatusOK, out)
}

// GetMyRun godoc
//
//	@Summary     Get one of my AI suggestion runs
//	@Description Returns the run metadata plus every pipeline event emitted during execution (prompt, AI responses, enrichment decisions, read_next matches, backfill passes).
//	@Tags        me,ai
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path    string  true  "Run ID"
//	@Success     200  {object}  object{run=handlers.RunView,events=[]handlers.EventView}
//	@Failure     404  {object}  object{error=string}
//	@Router      /me/suggestions/runs/{id} [get]
func (h *AISuggestionsHandler) GetMyRun(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	run, err := h.repo.GetRun(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "run not found")
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	if run.UserID != claims.UserID {
		respond.Error(w, http.StatusNotFound, "run not found")
		return
	}
	events, err := h.repo.ListEventsByRun(r.Context(), id)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"run":    runToView(run, false),
		"events": toEventViews(events),
	})
}

// AdminListRuns godoc
//
//	@Summary     List recent AI suggestion runs across all users (admin)
//	@Description Returns the most recent suggestion runs with cost, status, and owning user. Used by the admin jobs page.
//	@Tags        admin,jobs
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {array}  handlers.RunView
//	@Router      /admin/jobs/ai-suggestions/runs [get]
func (h *AISuggestionsHandler) AdminListRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := h.repo.ListRecentRuns(r.Context(), 50)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]RunView, 0, len(runs))
	for _, run := range runs {
		out = append(out, runToView(run, true))
	}
	respond.JSON(w, http.StatusOK, out)
}

// AdminGetRun godoc
//
//	@Summary     Get any AI suggestion run (admin)
//	@Description Returns the run metadata plus every pipeline event. Admin can view any user's run.
//	@Tags        admin,jobs
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path    string  true  "Run ID"
//	@Success     200  {object}  object{run=handlers.RunView,events=[]handlers.EventView}
//	@Failure     404  {object}  object{error=string}
//	@Router      /admin/jobs/ai-suggestions/runs/{id} [get]
func (h *AISuggestionsHandler) AdminGetRun(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	run, err := h.repo.GetRun(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "run not found")
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	events, err := h.repo.ListEventsByRun(r.Context(), id)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"run":    runToView(run, true),
		"events": toEventViews(events),
	})
}

func toEventViews(events []*models.AIRunEvent) []EventView {
	out := make([]EventView, 0, len(events))
	for _, e := range events {
		content := e.Content
		if len(content) == 0 {
			content = json.RawMessage("{}")
		}
		out = append(out, EventView{
			Seq:       e.Seq,
			Type:      e.Type,
			Content:   content,
			CreatedAt: e.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return out
}

// AdminRunSuggestions godoc
//
//	@Summary     Trigger a suggestions run for every opted-in user (admin)
//	@Description Enqueues a job per opted-in user.
//	@Tags        admin,jobs
//	@Produce     json
//	@Security    BearerAuth
//	@Success     202  {object}  object{enqueued=integer}
//	@Router      /admin/jobs/ai-suggestions/run [post]
func (h *AISuggestionsHandler) AdminRunSuggestions(w http.ResponseWriter, r *http.Request) {
	users, err := h.repo.ListOptedInUsers(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	enqueued := 0
	for _, u := range users {
		if _, err := h.riverClient.Insert(r.Context(),
			models.AISuggestionsJobArgs{UserID: u.UserID, TriggeredBy: "admin"}, nil); err != nil {
			continue
		}
		enqueued++
	}
	respond.JSON(w, http.StatusAccepted, map[string]any{"enqueued": enqueued})
}

