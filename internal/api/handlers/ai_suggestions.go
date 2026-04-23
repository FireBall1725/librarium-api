// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"context"
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
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// AISuggestionsHandler groups user-facing suggestion endpoints: list, change
// status (dismiss / interested / added_to_library), block, and run-now. aiSvc
// is consulted by the quota endpoint to surface feature availability; it
// provides the live registry so we can tell "no provider configured" apart
// from "job disabled by admin" without duplicating the registry's state.
type AISuggestionsHandler struct {
	repo        *repository.AISuggestionsRepo
	riverClient *river.Client[pgx.Tx]
	jobSvc      *service.JobService
	aiSvc       *service.AIService
}

func NewAISuggestionsHandler(repo *repository.AISuggestionsRepo, riverClient *river.Client[pgx.Tx], jobSvc *service.JobService, aiSvc *service.AIService) *AISuggestionsHandler {
	return &AISuggestionsHandler{repo: repo, riverClient: riverClient, jobSvc: jobSvc, aiSvc: aiSvc}
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
//	@Description Returns the current AI-generated recommendations for the caller. Filter by type (buy | read_next) and status, or scope to a specific run via `run_id`. When `run_id` is set the status filter is ignored — the scoped view returns every suggestion that run produced.
//	@Tags        me,ai
//	@Produce     json
//	@Security    BearerAuth
//	@Param       type    query     string  false  "Filter by type: buy | read_next"
//	@Param       status  query     string  false  "Filter by status: new | dismissed | interested | added_to_library"
//	@Param       run_id  query     string  false  "Scope to a specific run ID"
//	@Success     200     {array}   handlers.SuggestionView
//	@Failure     400     {object}  object{error=string}
//	@Failure     404     {object}  object{error=string}
//	@Router      /me/suggestions [get]
func (h *AISuggestionsHandler) ListSuggestions(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	typeFilter := r.URL.Query().Get("type")
	statusFilter := r.URL.Query().Get("status")

	// since accepts either an RFC3339 timestamp or a relative token like "30d"
	// / "7d". A missing or empty value means no window. The UI uses "30d" on
	// initial load and passes nothing when the user clicks "Show older".
	var sincePtr *time.Time
	if s := r.URL.Query().Get("since"); s != "" {
		if d, ok := parseRelativeDays(s); ok {
			t := time.Now().Add(-time.Duration(d) * 24 * time.Hour)
			sincePtr = &t
		} else if t, err := time.Parse(time.RFC3339, s); err == nil {
			sincePtr = &t
		} else {
			respond.Error(w, http.StatusBadRequest, "invalid since: expected RFC3339 or NNd")
			return
		}
	}

	var bookIDPtr *uuid.UUID
	if s := r.URL.Query().Get("book_id"); s != "" {
		bookID, err := uuid.Parse(s)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid book_id")
			return
		}
		bookIDPtr = &bookID
	}

	var runIDPtr *uuid.UUID
	if s := r.URL.Query().Get("run_id"); s != "" {
		runID, err := uuid.Parse(s)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid run_id")
			return
		}
		// Confirm the run belongs to the caller before we let them peek at it.
		// Without this, a user could enumerate anyone's suggestions by guessing
		// run IDs (admittedly 128-bit UUIDs, but still not a boundary to lean on).
		run, err := h.repo.GetRun(r.Context(), runID)
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
		runIDPtr = &runID
	} else if statusFilter == "" {
		statusFilter = "new"
	}

	// When filtering by book_id, clear the default status=new fallback so the
	// caller sees every suggestion they have for the book (including
	// interested, dismissed, etc.). The BookDetailPage needs that to decide
	// whether to offer "Remove suggestion".
	if bookIDPtr != nil && r.URL.Query().Get("status") == "" {
		statusFilter = ""
	}
	items, err := h.repo.ListSuggestions(r.Context(), claims.UserID, typeFilter, statusFilter, runIDPtr, sincePtr, bookIDPtr)
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

// DeleteSuggestion godoc
//
//	@Summary     Remove a suggestion
//	@Description Hard-deletes a single suggestion. Used by both the "Remove"
//	@Description button on SuggestionCard / BookDetailPage and the cleaned-up
//	@Description Dismiss flow (dismiss collapses into delete per the
//	@Description suggestions-as-books plan).
//	@Tags        me,ai
//	@Security    BearerAuth
//	@Param       id    path      string  true  "Suggestion ID"
//	@Success     204
//	@Failure     400   {object}  object{error=string}
//	@Failure     404   {object}  object{error=string}
//	@Router      /me/suggestions/{id} [delete]
func (h *AISuggestionsHandler) DeleteSuggestion(w http.ResponseWriter, r *http.Request) {
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
	if err := h.repo.DeleteSuggestion(r.Context(), id, claims.UserID); err != nil {
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
//	@Description Enqueues a background job to regenerate suggestions. Accepts an optional `steering` body to bias this run toward specific authors, series, genres, tags, and/or free-form notes. Rate-limited by admin config.
//	@Tags        me,ai
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body  body      object{steering=models.SuggestionSteering}  false  "Optional steering payload"
//	@Success     202
//	@Failure     400  {object}  object{error=string}
//	@Failure     429  {object}  object{error=string}
//	@Router      /me/suggestions/run [post]
func (h *AISuggestionsHandler) RunNow(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	// Body is optional — legacy clients that POST with no body still get a
	// normal unsteered run. We only decode if Content-Length suggests content
	// so an empty body doesn't surface a decode error.
	var body struct {
		Steering *models.SuggestionSteering `json:"steering,omitempty"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	// Collapse a wholly-empty steering object to nil so the worker treats it
	// as an unsteered run — avoids persisting '{}' on the row and simplifies
	// the prompt branch.
	if body.Steering != nil && body.Steering.IsEmpty() {
		body.Steering = nil
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
	// Reject the enqueue outright if a run is already in flight — stacking a
	// second job would just race the first for the same user.
	running, err := h.repo.CountRunningRunsForUser(r.Context(), claims.UserID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	if running > 0 {
		respond.Error(w, http.StatusConflict, "a suggestions run is already in progress")
		return
	}
	if _, err := h.riverClient.Insert(r.Context(),
		models.AISuggestionsJobArgs{UserID: claims.UserID, TriggeredBy: "user", Steering: body.Steering}, nil); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// QuotaView is the JSON shape for the caller's daily run quota. Limit = 0
// means unlimited. Available=false means the feature can't be used right now
// regardless of quota — UnavailableReason explains why so clients can render
// the right inline hint (and decide whether to hide the sidebar entry).
//
// Unavailable reasons, in the order the server resolves them:
//   - "job_disabled"  → admin disabled the AI suggestions job instance-wide
//   - "no_provider"   → no active AI provider configured on the instance
//   - "not_opted_in"  → user hasn't opted in under Profile → AI Privacy
type QuotaView struct {
	Used              int    `json:"used"`
	Limit             int    `json:"limit"`
	ResetsAt          string `json:"resets_at,omitempty"`
	Unlimited         bool   `json:"unlimited"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

// GetMyQuota godoc
//
//	@Summary     Get my AI suggestion run quota
//	@Description Returns how many suggestion runs the caller has used in the last 24h and the configured per-user daily limit.
//	@Tags        me,ai
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {object}  handlers.QuotaView
//	@Router      /me/suggestions/quota [get]
func (h *AISuggestionsHandler) GetMyQuota(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	cfg, err := h.jobSvc.GetAISuggestionsConfig(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	used, err := h.repo.RunsInLast24h(r.Context(), claims.UserID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	// Feature-availability resolution — order is deliberate. Admin-wide
	// disables win over per-user state: if the admin turned the job off, we
	// shouldn't prompt the user to toggle their opt-in as if that would help.
	available := true
	reason := ""
	switch {
	case !cfg.Enabled:
		available, reason = false, "job_disabled"
	case h.aiSvc == nil || h.aiSvc.Registry().Active() == nil:
		available, reason = false, "no_provider"
	default:
		optedIn, err := h.repo.IsOptedIn(r.Context(), claims.UserID)
		if err != nil {
			respond.ServerError(w, r, err)
			return
		}
		if !optedIn {
			available, reason = false, "not_opted_in"
		}
	}

	// -1 = unlimited, 0 = disabled, positive = cap. The UI renders "unlimited"
	// copy when the flag is set and hides the counter; limit=0 surfaces as
	// 0 remaining, which correctly blocks user-triggered runs.
	out := QuotaView{
		Used:              used,
		Limit:             cfg.UserRunRateLimitPerDay,
		Unlimited:         cfg.UserRunRateLimitPerDay < 0,
		Available:         available,
		UnavailableReason: reason,
	}
	// resets_at = earliest-in-window + 24h — when the oldest run falls out of
	// the rolling window and the user gets a slot back. Only meaningful when a
	// limit is configured and the user has runs in the window.
	if !out.Unlimited && used > 0 {
		earliest, err := h.repo.EarliestRunStartInLast24h(r.Context(), claims.UserID)
		if err != nil {
			respond.ServerError(w, r, err)
			return
		}
		if !earliest.IsZero() {
			out.ResetsAt = earliest.Add(24 * time.Hour).Format("2006-01-02T15:04:05Z07:00")
		}
	}
	respond.JSON(w, http.StatusOK, out)
}

// ─── Run observability ────────────────────────────────────────────────────────

// RunView is the JSON shape for a single suggestions run — shared by user and
// admin endpoints. UserID is only populated for admin-scoped responses.
// Steering is present only on runs the user triggered with a custom ask and
// comes back with display names resolved, so the UI doesn't need a second
// round-trip to render the steering summary banner.
type RunView struct {
	ID               uuid.UUID     `json:"id"`
	UserID           string        `json:"user_id,omitempty"`
	TriggeredBy      string        `json:"triggered_by"`
	ProviderType     string        `json:"provider_type"`
	ModelID          string        `json:"model_id,omitempty"`
	Status           string        `json:"status"`
	Error            string        `json:"error,omitempty"`
	TokensIn         int           `json:"tokens_in"`
	TokensOut        int           `json:"tokens_out"`
	EstimatedCostUSD float64       `json:"estimated_cost_usd"`
	SuggestionCount  int           `json:"suggestion_count"`
	StartedAt        string        `json:"started_at"`
	FinishedAt       string        `json:"finished_at,omitempty"`
	Steering         *SteeringView `json:"steering,omitempty"`
}

// SteeringView hydrates stored steering IDs to {id, name} objects for direct
// UI consumption. Notes is free-form text and flows through unchanged.
type SteeringView struct {
	Authors []NamedRef        `json:"authors,omitempty"`
	Series  []NamedRef        `json:"series,omitempty"`
	Genres  []NamedRef        `json:"genres,omitempty"`
	Tags    []NamedTagRef     `json:"tags,omitempty"`
	Notes   string            `json:"notes,omitempty"`
}

type NamedRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type NamedTagRef struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	LibraryID uuid.UUID `json:"library_id"`
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
		SuggestionCount:  r.SuggestionCount,
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

// hydrateSteeringViews fills RunView.Steering for every run with a non-null
// steering column. Batches lookups per taxonomy so N runs cost at most 4 SQL
// queries instead of 4N. Stale IDs (entities deleted after the ask) are
// quietly dropped; the notes field always survives intact.
func (h *AISuggestionsHandler) hydrateSteeringViews(ctx context.Context, runs []*models.AISuggestionRun, views []RunView) error {
	if len(views) == 0 {
		return nil
	}
	var authorIDs, seriesIDs, genreIDs, tagIDs []uuid.UUID
	decoded := make([]*models.SuggestionSteering, len(runs))
	for i, r := range runs {
		if len(r.Steering) == 0 {
			continue
		}
		var s models.SuggestionSteering
		if err := json.Unmarshal(r.Steering, &s); err != nil {
			// A row with malformed steering shouldn't poison the whole list —
			// log-and-skip so the rest of the timeline renders. Empty stays nil.
			continue
		}
		decoded[i] = &s
		authorIDs = append(authorIDs, s.AuthorIDs...)
		seriesIDs = append(seriesIDs, s.SeriesIDs...)
		genreIDs = append(genreIDs, s.GenreIDs...)
		tagIDs = append(tagIDs, s.TagIDs...)
	}

	authorNames, err := h.repo.ResolveNames(ctx, "contributors", dedupeUUIDs(authorIDs))
	if err != nil {
		return err
	}
	seriesNames, err := h.repo.ResolveNames(ctx, "series", dedupeUUIDs(seriesIDs))
	if err != nil {
		return err
	}
	genreNames, err := h.repo.ResolveNames(ctx, "genres", dedupeUUIDs(genreIDs))
	if err != nil {
		return err
	}
	tagRefs, err := h.repo.ResolveTags(ctx, dedupeUUIDs(tagIDs))
	if err != nil {
		return err
	}

	for i, s := range decoded {
		if s == nil {
			continue
		}
		sv := &SteeringView{Notes: s.Notes}
		for _, id := range s.AuthorIDs {
			if name, ok := authorNames[id]; ok {
				sv.Authors = append(sv.Authors, NamedRef{ID: id, Name: name})
			}
		}
		for _, id := range s.SeriesIDs {
			if name, ok := seriesNames[id]; ok {
				sv.Series = append(sv.Series, NamedRef{ID: id, Name: name})
			}
		}
		for _, id := range s.GenreIDs {
			if name, ok := genreNames[id]; ok {
				sv.Genres = append(sv.Genres, NamedRef{ID: id, Name: name})
			}
		}
		for _, id := range s.TagIDs {
			if t, ok := tagRefs[id]; ok {
				sv.Tags = append(sv.Tags, NamedTagRef{ID: id, Name: t.Name, LibraryID: t.LibraryID})
			}
		}
		// Wholly-stale payloads with no notes collapse to omitted — same signal
		// as an unsteered run, which is the right read once nothing's left.
		if len(sv.Authors) == 0 && len(sv.Series) == 0 && len(sv.Genres) == 0 && len(sv.Tags) == 0 && sv.Notes == "" {
			continue
		}
		views[i].Steering = sv
	}
	return nil
}

func dedupeUUIDs(ids []uuid.UUID) []uuid.UUID {
	if len(ids) < 2 {
		return ids
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
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
	out := make([]RunView, len(runs))
	for i, run := range runs {
		out[i] = runToView(run, false)
	}
	if err := h.hydrateSteeringViews(r.Context(), runs, out); err != nil {
		respond.ServerError(w, r, err)
		return
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
	views := []RunView{runToView(run, false)}
	if err := h.hydrateSteeringViews(r.Context(), []*models.AISuggestionRun{run}, views); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"run":    views[0],
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
	out := make([]RunView, len(runs))
	for i, run := range runs {
		out[i] = runToView(run, true)
	}
	if err := h.hydrateSteeringViews(r.Context(), runs, out); err != nil {
		respond.ServerError(w, r, err)
		return
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
	// Events filter by the real run id — the path id may have been an
	// umbrella job id (resolved by GetRun above).
	events, err := h.repo.ListEventsByRun(r.Context(), run.ID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	views := []RunView{runToView(run, true)}
	if err := h.hydrateSteeringViews(r.Context(), []*models.AISuggestionRun{run}, views); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"run":    views[0],
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
	enqueued, skipped := 0, 0
	for _, u := range users {
		// Skip users who already have a run in flight — enqueuing a second
		// would just race the first.
		n, err := h.repo.CountRunningRunsForUser(r.Context(), u.UserID)
		if err == nil && n > 0 {
			skipped++
			continue
		}
		if _, err := h.riverClient.Insert(r.Context(),
			models.AISuggestionsJobArgs{UserID: u.UserID, TriggeredBy: "admin"}, nil); err != nil {
			continue
		}
		enqueued++
	}
	respond.JSON(w, http.StatusAccepted, map[string]any{"enqueued": enqueued, "skipped": skipped})
}

// AdminCancelRun godoc
//
//	@Summary     Cancel a running AI suggestion run (admin)
//	@Description Marks a running run as cancelled; the worker checks status between stages and exits on the next check. Completed or already-cancelled runs return 404.
//	@Tags        admin,jobs
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path   string  true  "Run ID"
//	@Success     204
//	@Failure     404  {object}  object{error=string}
//	@Router      /admin/jobs/ai-suggestions/runs/{id} [delete]
func (h *AISuggestionsHandler) AdminCancelRun(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.repo.CancelRun(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "run not found or not running")
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AdminClearFinishedRuns godoc
//
//	@Summary     Delete all finished AI suggestion runs (admin)
//	@Description Removes every run in a terminal state (completed, failed, cancelled). Running runs are left alone.
//	@Tags        admin,jobs
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200  {object}  object{deleted=integer}
//	@Router      /admin/jobs/ai-suggestions/runs [delete]
func (h *AISuggestionsHandler) AdminClearFinishedRuns(w http.ResponseWriter, r *http.Request) {
	deleted, err := h.repo.DeleteFinishedRuns(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

// parseRelativeDays accepts tokens like "30d", "7d" and returns the integer
// day count. Returns (0, false) for anything else.
func parseRelativeDays(s string) (int, bool) {
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

