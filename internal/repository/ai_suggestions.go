// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AISuggestionsRepo struct {
	db *pgxpool.Pool
}

func NewAISuggestionsRepo(db *pgxpool.Pool) *AISuggestionsRepo {
	return &AISuggestionsRepo{db: db}
}

// ─── Opted-in users & library data ────────────────────────────────────────────

// OptedInUser is the minimum shape the worker needs to decide whether to run
// for a user and which library to load from.
type OptedInUser struct {
	UserID       uuid.UUID
	LibraryID    uuid.UUID
	TasteProfile json.RawMessage
}

// ListOptedInUsers returns every user with opt_in=true who has at least one
// library they're a member of. We pick one library per user (the oldest they
// were added to) since household libraries are shared.
func (r *AISuggestionsRepo) ListOptedInUsers(ctx context.Context) ([]*OptedInUser, error) {
	const q = `
		SELECT u.id, s.taste_profile,
			(
				SELECT lm.library_id
				FROM library_memberships lm
				WHERE lm.user_id = u.id
				ORDER BY lm.joined_at ASC
				LIMIT 1
			) AS library_id
		FROM users u
		JOIN user_ai_settings s ON s.user_id = u.id
		WHERE s.opt_in = TRUE AND u.is_active = TRUE`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list opted-in users: %w", err)
	}
	defer rows.Close()
	var out []*OptedInUser
	for rows.Next() {
		u := &OptedInUser{}
		var lib *uuid.UUID
		if err := rows.Scan(&u.UserID, &u.TasteProfile, &lib); err != nil {
			return nil, err
		}
		if lib == nil {
			continue // no library → no signal, skip
		}
		u.LibraryID = *lib
		if len(u.TasteProfile) == 0 {
			u.TasteProfile = json.RawMessage("{}")
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// IsOptedIn reports whether the user has opted into AI features. Distinct
// from GetOptedInUser: this one doesn't require the user to have any library,
// because the quota endpoint wants to separate "not opted in" from "no
// library yet" when reporting availability.
func (r *AISuggestionsRepo) IsOptedIn(ctx context.Context, userID uuid.UUID) (bool, error) {
	const q = `SELECT COALESCE((SELECT opt_in FROM user_ai_settings WHERE user_id = $1), FALSE)`
	var ok bool
	if err := r.db.QueryRow(ctx, q, userID).Scan(&ok); err != nil {
		return false, fmt.Errorf("check opt-in: %w", err)
	}
	return ok, nil
}

// GetOptedInUser returns a single user's opt-in row + library if present.
// Returns nil (no error) if the user isn't opted in or has no library.
func (r *AISuggestionsRepo) GetOptedInUser(ctx context.Context, userID uuid.UUID) (*OptedInUser, error) {
	const q = `
		SELECT u.id, s.taste_profile,
			(
				SELECT lm.library_id
				FROM library_memberships lm
				WHERE lm.user_id = u.id
				ORDER BY lm.joined_at ASC
				LIMIT 1
			) AS library_id
		FROM users u
		JOIN user_ai_settings s ON s.user_id = u.id
		WHERE u.id = $1 AND s.opt_in = TRUE AND u.is_active = TRUE`
	u := &OptedInUser{}
	var lib *uuid.UUID
	err := r.db.QueryRow(ctx, q, userID).Scan(&u.UserID, &u.TasteProfile, &lib)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get opted-in user: %w", err)
	}
	if lib == nil {
		return nil, nil
	}
	u.LibraryID = *lib
	if len(u.TasteProfile) == 0 {
		u.TasteProfile = json.RawMessage("{}")
	}
	return u, nil
}

// LibraryTitle is a compact book row for prompt construction.
type LibraryTitle struct {
	BookID     uuid.UUID
	LibraryID  uuid.UUID
	Title      string
	Author     string
	MediaType  string
	GenreNames []string
	Rating     *int   // user's rating, 1–5, nil if never rated
	ReadStatus string // unread | reading | read | did_not_finish
	IsFavorite bool
	SeriesName string
	HasCover   bool
	UpdatedAt  time.Time // used as a cache-buster on the cover URL
}

// ListLibraryTitles returns every book in a library annotated with the caller's
// ratings, read_status, and favourite flag. This is the raw material for the
// prompt.
func (r *AISuggestionsRepo) ListLibraryTitles(ctx context.Context, libraryID, userID uuid.UUID) ([]*LibraryTitle, error) {
	const q = `
		SELECT
			b.id,
			b.library_id,
			b.title,
			COALESCE((
				SELECT c.name
				FROM book_contributors bc
				JOIN contributors c ON c.id = bc.contributor_id
				WHERE bc.book_id = b.id AND bc.role = 'author'
				ORDER BY bc.display_order
				LIMIT 1
			), '') AS author,
			COALESCE(mt.display_name, ''),
			COALESCE((
				SELECT string_agg(g.name, ',' ORDER BY g.name)
				FROM book_genres bg JOIN genres g ON g.id = bg.genre_id
				WHERE bg.book_id = b.id
			), ''),
			(
				SELECT ubi.rating FROM book_editions be
				JOIN user_book_interactions ubi ON ubi.book_edition_id = be.id
				WHERE be.book_id = b.id AND ubi.user_id = $2 AND ubi.rating IS NOT NULL
				ORDER BY ubi.rating DESC
				LIMIT 1
			),
			COALESCE((
				SELECT ubi.read_status FROM book_editions be
				JOIN user_book_interactions ubi ON ubi.book_edition_id = be.id
				WHERE be.book_id = b.id AND ubi.user_id = $2
				ORDER BY CASE ubi.read_status
					WHEN 'read' THEN 1 WHEN 'reading' THEN 2
					WHEN 'did_not_finish' THEN 3 ELSE 4 END
				LIMIT 1
			), 'unread'),
			COALESCE((
				SELECT bool_or(ubi.is_favorite) FROM book_editions be
				JOIN user_book_interactions ubi ON ubi.book_edition_id = be.id
				WHERE be.book_id = b.id AND ubi.user_id = $2
			), FALSE),
			COALESCE((
				SELECT s.name FROM book_series bs
				JOIN series s ON s.id = bs.series_id
				WHERE bs.book_id = b.id
				ORDER BY bs.position LIMIT 1
			), ''),
			EXISTS(
				SELECT 1 FROM cover_images ci
				WHERE ci.entity_type = 'book' AND ci.entity_id = b.id AND ci.is_primary = TRUE
			) AS has_cover,
			b.updated_at
		FROM books b
		LEFT JOIN media_types mt ON mt.id = b.media_type_id
		WHERE b.library_id = $1
		ORDER BY b.title`
	rows, err := r.db.Query(ctx, q, libraryID, userID)
	if err != nil {
		return nil, fmt.Errorf("list library titles: %w", err)
	}
	defer rows.Close()
	var out []*LibraryTitle
	for rows.Next() {
		t := &LibraryTitle{}
		var genres string
		if err := rows.Scan(
			&t.BookID, &t.LibraryID, &t.Title, &t.Author, &t.MediaType, &genres,
			&t.Rating, &t.ReadStatus, &t.IsFavorite, &t.SeriesName,
			&t.HasCover, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if genres != "" {
			t.GenreNames = splitCSV(genres)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// BookExistsInLibrary is used to reject `buy` candidates the user already owns.
// Matches by ISBN first (exact); falls back to case-insensitive title when ISBN
// is blank.
func (r *AISuggestionsRepo) BookExistsInLibrary(ctx context.Context, libraryID uuid.UUID, title, isbn string) (bool, error) {
	if isbn != "" {
		const q = `
			SELECT EXISTS (
				SELECT 1 FROM book_editions be
				JOIN books b ON b.id = be.book_id
				WHERE b.library_id = $1 AND (be.isbn_13 = $2 OR be.isbn_10 = $2)
			)`
		var ok bool
		if err := r.db.QueryRow(ctx, q, libraryID, isbn).Scan(&ok); err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	if title == "" {
		return false, nil
	}
	const qTitle = `
		SELECT EXISTS (
			SELECT 1 FROM books b
			WHERE b.library_id = $1 AND lower(b.title) = lower($2)
		)`
	var ok bool
	if err := r.db.QueryRow(ctx, qTitle, libraryID, title).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// ─── Runs ─────────────────────────────────────────────────────────────────────

// CreateRun inserts a run row in 'running' state and returns its ID. steering
// is the raw JSON payload persisted on the row (nil for unsteered runs); pass
// it pre-marshalled so this helper stays taxonomy-agnostic.
func (r *AISuggestionsRepo) CreateRun(ctx context.Context, userID uuid.UUID, triggeredBy, providerType, modelID string, steering []byte) (uuid.UUID, error) {
	const q = `
		INSERT INTO ai_suggestion_runs (user_id, triggered_by, provider_type, model_id, status, steering)
		VALUES ($1, $2, $3, $4, 'running', $5) RETURNING id`
	var id uuid.UUID
	var steeringArg any
	if len(steering) > 0 {
		steeringArg = steering
	}
	if err := r.db.QueryRow(ctx, q, userID, triggeredBy, providerType, modelID, steeringArg).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("create run: %w", err)
	}
	return id, nil
}

// FinishRun marks a run complete (or failed) and records usage totals.
func (r *AISuggestionsRepo) FinishRun(ctx context.Context, runID uuid.UUID, status, errMsg string, tokensIn, tokensOut int, costUSD float64) error {
	const q = `
		UPDATE ai_suggestion_runs
		SET status = $2, error = $3, tokens_in = $4, tokens_out = $5,
		    estimated_cost_usd = $6, finished_at = $7
		WHERE id = $1`
	_, err := r.db.Exec(ctx, q, runID, status, nilIfEmpty(errMsg), tokensIn, tokensOut, costUSD, time.Now())
	if err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	return nil
}

// LastRunAt returns the most recent finished/running run timestamp for a user,
// or zero time if none exist. Used by the scheduler to decide when to enqueue.
func (r *AISuggestionsRepo) LastRunAt(ctx context.Context, userID uuid.UUID) (time.Time, error) {
	const q = `SELECT MAX(started_at) FROM ai_suggestion_runs WHERE user_id = $1`
	var t *time.Time
	if err := r.db.QueryRow(ctx, q, userID).Scan(&t); err != nil {
		return time.Time{}, err
	}
	if t == nil {
		return time.Time{}, nil
	}
	return *t, nil
}

// RunsInLast24h counts user-triggered suggestion runs over the last 24h — used
// to enforce the per-user manual-run rate limit. Scheduled runs (triggered_by
// = 'scheduler') are intentionally excluded: the daily scheduler shouldn't
// burn against the user's Run Now budget.
func (r *AISuggestionsRepo) RunsInLast24h(ctx context.Context, userID uuid.UUID) (int, error) {
	const q = `
		SELECT COUNT(*) FROM ai_suggestion_runs
		WHERE user_id = $1 AND triggered_by = 'user'
		  AND started_at >= NOW() - INTERVAL '24 hours'`
	var n int
	if err := r.db.QueryRow(ctx, q, userID).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// EarliestRunStartInLast24h returns the earliest started_at within the rolling
// 24h window for the user's manual runs, used to compute when the quota will
// next drop. Matches the triggered_by filter in RunsInLast24h so the reset
// timestamp lines up with the counter. Zero time if none in window.
func (r *AISuggestionsRepo) EarliestRunStartInLast24h(ctx context.Context, userID uuid.UUID) (time.Time, error) {
	const q = `
		SELECT MIN(started_at) FROM ai_suggestion_runs
		WHERE user_id = $1 AND triggered_by = 'user'
		  AND started_at >= NOW() - INTERVAL '24 hours'`
	var t *time.Time
	if err := r.db.QueryRow(ctx, q, userID).Scan(&t); err != nil {
		return time.Time{}, err
	}
	if t == nil {
		return time.Time{}, nil
	}
	return *t, nil
}

// CountRunningRunsForUser returns how many of the user's runs are still in
// 'running' state. Used to prevent stacking parallel runs from /run-now.
func (r *AISuggestionsRepo) CountRunningRunsForUser(ctx context.Context, userID uuid.UUID) (int, error) {
	const q = `SELECT COUNT(*) FROM ai_suggestion_runs WHERE user_id = $1 AND status = 'running'`
	var n int
	if err := r.db.QueryRow(ctx, q, userID).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// GetRunStatus returns a run's current status. Used by the worker to poll for
// cooperative cancellation between pipeline stages.
func (r *AISuggestionsRepo) GetRunStatus(ctx context.Context, runID uuid.UUID) (string, error) {
	const q = `SELECT status FROM ai_suggestion_runs WHERE id = $1`
	var s string
	err := r.db.QueryRow(ctx, q, runID).Scan(&s)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return s, nil
}

// CancelRun marks a running run as 'cancelled'. Returns ErrNotFound when the
// run doesn't exist or is no longer running (idempotent for already-finished
// runs — cancelling a completed run is a no-op).
func (r *AISuggestionsRepo) CancelRun(ctx context.Context, runID uuid.UUID) error {
	const q = `
		UPDATE ai_suggestion_runs
		SET status = 'cancelled', finished_at = NOW(), error = 'cancelled'
		WHERE id = $1 AND status = 'running'`
	tag, err := r.db.Exec(ctx, q, runID)
	if err != nil {
		return fmt.Errorf("cancel run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteFinishedRuns removes every run in a terminal state (completed /
// failed / cancelled). Events cascade via FK. Returns the number of runs
// actually deleted.
func (r *AISuggestionsRepo) DeleteFinishedRuns(ctx context.Context) (int64, error) {
	const q = `DELETE FROM ai_suggestion_runs WHERE status IN ('completed', 'failed', 'cancelled')`
	tag, err := r.db.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("delete finished runs: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ─── Suggestions ──────────────────────────────────────────────────────────────

// SuggestionInput is the worker's view of a suggestion about to be persisted.
type SuggestionInput struct {
	Type          string // buy | read_next
	BookID        *uuid.UUID
	BookEditionID *uuid.UUID
	Title         string
	Author        string
	ISBN          string
	CoverURL      string
	Reasoning     string
}

// AppendSuggestions inserts new suggestions on top of what's already there,
// relying on the partial unique index on (user_id, type, lower(title)) to
// silently drop duplicates within the user's current 'new' pool. After
// inserting, the oldest 'new' rows of each type beyond their per-type cap are
// evicted so neither shelf grows unboundedly. Pass 0 for either cap to disable
// eviction for that type.
//
// Dismissed/interested/added rows from any run are preserved — they're filtered
// out of the user view by status, not by deletion.
func (r *AISuggestionsRepo) AppendSuggestions(ctx context.Context, userID, runID uuid.UUID, items []SuggestionInput, maxBuy, maxReadNext int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// created_at uses clock_timestamp() rather than the DEFAULT (NOW()) because
	// NOW() is transaction-start time — every row in this loop would otherwise
	// share the same timestamp and the "newer first" ORDER BY created_at DESC
	// would lose AI output order inside a single run.
	const ins = `
		INSERT INTO ai_suggestions (user_id, run_id, type, book_id, book_edition_id,
			title, author, isbn, cover_url, reasoning, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'new', clock_timestamp())
		ON CONFLICT DO NOTHING`
	for _, it := range items {
		if _, err := tx.Exec(ctx, ins, userID, runID, it.Type,
			it.BookID, it.BookEditionID,
			it.Title, nilIfEmpty(it.Author), nilIfEmpty(it.ISBN),
			nilIfEmpty(it.CoverURL), nilIfEmpty(it.Reasoning),
		); err != nil {
			return fmt.Errorf("insert suggestion: %w", err)
		}
	}

	const evict = `
		DELETE FROM ai_suggestions
		WHERE id IN (
			SELECT id FROM ai_suggestions
			WHERE user_id = $1 AND status = 'new' AND type = $2
			ORDER BY created_at DESC
			OFFSET $3
		)`
	if maxBuy > 0 {
		if _, err := tx.Exec(ctx, evict, userID, "buy", maxBuy); err != nil {
			return fmt.Errorf("evict oldest buy: %w", err)
		}
	}
	if maxReadNext > 0 {
		if _, err := tx.Exec(ctx, evict, userID, "read_next", maxReadNext); err != nil {
			return fmt.Errorf("evict oldest read_next: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// ListNewSuggestionKeys returns the normalized-title set of the user's current
// 'new' suggestions. Used by the service to dedupe a new run's candidates
// against what's already in the pool, so a backfill pass doesn't churn on a
// title the unique index would silently drop anyway.
func (r *AISuggestionsRepo) ListNewSuggestionKeys(ctx context.Context, userID uuid.UUID) (map[string]struct{}, error) {
	const q = `SELECT lower(title) FROM ai_suggestions WHERE user_id = $1 AND status = 'new'`
	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list new keys: %w", err)
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out[t] = struct{}{}
	}
	return out, rows.Err()
}

// ListSuggestions returns the caller's current suggestions. Filter by type
// ('buy' | 'read_next' | '' for all), status ('new' | '' for all), and
// optionally scope to a specific run (non-nil runID). When runID is set, the
// status filter is ignored — scoped views surface every suggestion the run
// produced, including ones the user later dismissed or saved.
func (r *AISuggestionsRepo) ListSuggestions(ctx context.Context, userID uuid.UUID, typeFilter, statusFilter string, runID *uuid.UUID) ([]*models.AISuggestionWithLibrary, error) {
	// LEFT JOIN on books so read_next suggestions (which point into the user's
	// library) can surface library_id for direct navigation. buy-type rows have
	// book_id = NULL so the join just returns NULL for them.
	q := `
		SELECT s.id, s.user_id, s.run_id, s.type, s.book_id, s.book_edition_id,
		       s.title, COALESCE(s.author,''), COALESCE(s.isbn,''), COALESCE(s.cover_url,''),
		       COALESCE(s.reasoning,''), s.status, s.created_at, b.library_id
		FROM ai_suggestions s
		LEFT JOIN books b ON b.id = s.book_id
		WHERE s.user_id = $1`
	args := []any{userID}
	if typeFilter != "" {
		q += fmt.Sprintf(" AND s.type = $%d", len(args)+1)
		args = append(args, typeFilter)
	}
	if runID != nil {
		q += fmt.Sprintf(" AND s.run_id = $%d", len(args)+1)
		args = append(args, *runID)
	} else if statusFilter != "" {
		q += fmt.Sprintf(" AND s.status = $%d", len(args)+1)
		args = append(args, statusFilter)
	}
	q += " ORDER BY s.created_at DESC"
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list suggestions: %w", err)
	}
	defer rows.Close()
	var out []*models.AISuggestionWithLibrary
	for rows.Next() {
		s := &models.AISuggestionWithLibrary{}
		if err := rows.Scan(
			&s.ID, &s.UserID, &s.RunID, &s.Type,
			&s.BookID, &s.BookEditionID,
			&s.Title, &s.Author, &s.ISBN, &s.CoverURL,
			&s.Reasoning, &s.Status, &s.CreatedAt, &s.LibraryID,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateSuggestionStatus changes the user-visible status flag. Returns
// ErrNotFound if the row doesn't exist or doesn't belong to the caller.
func (r *AISuggestionsRepo) UpdateSuggestionStatus(ctx context.Context, id, userID uuid.UUID, status string) error {
	const q = `UPDATE ai_suggestions SET status = $1 WHERE id = $2 AND user_id = $3`
	tag, err := r.db.Exec(ctx, q, status, id, userID)
	if err != nil {
		return fmt.Errorf("update suggestion status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetSuggestion fetches one suggestion scoped to a user (for the block flow,
// which needs title/author/isbn to persist).
func (r *AISuggestionsRepo) GetSuggestion(ctx context.Context, id, userID uuid.UUID) (*models.AISuggestion, error) {
	const q = `
		SELECT id, user_id, run_id, type, book_id, book_edition_id,
		       title, COALESCE(author,''), COALESCE(isbn,''), COALESCE(cover_url,''),
		       COALESCE(reasoning,''), status, created_at
		FROM ai_suggestions WHERE id = $1 AND user_id = $2`
	s := &models.AISuggestion{}
	err := r.db.QueryRow(ctx, q, id, userID).Scan(
		&s.ID, &s.UserID, &s.RunID, &s.Type,
		&s.BookID, &s.BookEditionID,
		&s.Title, &s.Author, &s.ISBN, &s.CoverURL,
		&s.Reasoning, &s.Status, &s.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

// ─── Run events (observability) ───────────────────────────────────────────────

// AppendEvent writes one pipeline event tied to a run. Sequence is computed
// server-side via COALESCE(MAX(seq)+1, 0) so callers don't race each other.
// content is marshaled to JSON; nil is stored as '{}'.
func (r *AISuggestionsRepo) AppendEvent(ctx context.Context, runID uuid.UUID, eventType string, content any) error {
	var payload []byte
	if content == nil {
		payload = []byte("{}")
	} else if b, ok := content.([]byte); ok {
		payload = b
	} else {
		b, err := json.Marshal(content)
		if err != nil {
			return fmt.Errorf("marshal event content: %w", err)
		}
		payload = b
	}
	const q = `
		INSERT INTO ai_run_events (run_id, seq, type, content)
		VALUES ($1,
		        COALESCE((SELECT MAX(seq) + 1 FROM ai_run_events WHERE run_id = $1), 0),
		        $2, $3::jsonb)`
	if _, err := r.db.Exec(ctx, q, runID, eventType, payload); err != nil {
		return fmt.Errorf("append run event: %w", err)
	}
	return nil
}

// ListEventsByRun returns every event recorded for a run, ordered by seq.
func (r *AISuggestionsRepo) ListEventsByRun(ctx context.Context, runID uuid.UUID) ([]*models.AIRunEvent, error) {
	const q = `
		SELECT id, run_id, seq, type, content, created_at
		FROM ai_run_events WHERE run_id = $1 ORDER BY seq ASC`
	rows, err := r.db.Query(ctx, q, runID)
	if err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}
	defer rows.Close()
	var out []*models.AIRunEvent
	for rows.Next() {
		e := &models.AIRunEvent{}
		if err := rows.Scan(&e.ID, &e.RunID, &e.Seq, &e.Type, &e.Content, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetRun fetches a single run row by ID. Returns ErrNotFound when missing.
// Callers that need per-user scoping should check UserID after fetching.
func (r *AISuggestionsRepo) GetRun(ctx context.Context, runID uuid.UUID) (*models.AISuggestionRun, error) {
	const q = `
		SELECT id, user_id, triggered_by, provider_type, model_id, status,
		       COALESCE(error,''), tokens_in, tokens_out, estimated_cost_usd,
		       started_at, finished_at, steering
		FROM ai_suggestion_runs WHERE id = $1`
	run := &models.AISuggestionRun{}
	err := r.db.QueryRow(ctx, q, runID).Scan(
		&run.ID, &run.UserID, &run.TriggeredBy, &run.ProviderType, &run.ModelID,
		&run.Status, &run.Error, &run.TokensIn, &run.TokensOut, &run.EstimatedCostUSD,
		&run.StartedAt, &run.FinishedAt, &run.Steering,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return run, nil
}

// ListRunsByUser returns the caller's most recent suggestion runs, newest first.
func (r *AISuggestionsRepo) ListRunsByUser(ctx context.Context, userID uuid.UUID, limit int) ([]*models.AISuggestionRun, error) {
	if limit <= 0 {
		limit = 25
	}
	const q = `
		SELECT id, user_id, triggered_by, provider_type, model_id, status,
		       COALESCE(error,''), tokens_in, tokens_out, estimated_cost_usd,
		       started_at, finished_at, steering
		FROM ai_suggestion_runs WHERE user_id = $1
		ORDER BY started_at DESC LIMIT $2`
	return scanRuns(r.db.Query(ctx, q, userID, limit))
}

// ListRecentRuns returns the most recent suggestion runs across every user.
// Admin-scoped — used by the jobs page.
func (r *AISuggestionsRepo) ListRecentRuns(ctx context.Context, limit int) ([]*models.AISuggestionRun, error) {
	if limit <= 0 {
		limit = 50
	}
	const q = `
		SELECT id, user_id, triggered_by, provider_type, model_id, status,
		       COALESCE(error,''), tokens_in, tokens_out, estimated_cost_usd,
		       started_at, finished_at, steering
		FROM ai_suggestion_runs
		ORDER BY started_at DESC LIMIT $1`
	return scanRuns(r.db.Query(ctx, q, limit))
}

func scanRuns(rows pgx.Rows, err error) ([]*models.AISuggestionRun, error) {
	if err != nil {
		return nil, fmt.Errorf("query runs: %w", err)
	}
	defer rows.Close()
	var out []*models.AISuggestionRun
	for rows.Next() {
		run := &models.AISuggestionRun{}
		if err := rows.Scan(
			&run.ID, &run.UserID, &run.TriggeredBy, &run.ProviderType, &run.ModelID,
			&run.Status, &run.Error, &run.TokensIn, &run.TokensOut, &run.EstimatedCostUSD,
			&run.StartedAt, &run.FinishedAt, &run.Steering,
		); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// ─── Blocked items ────────────────────────────────────────────────────────────

// AddBlock records a "never suggest this again" entry.
func (r *AISuggestionsRepo) AddBlock(ctx context.Context, item models.AIBlockedItem) error {
	const q = `
		INSERT INTO ai_blocked_items (user_id, scope, title, author, isbn, series_id, series_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := r.db.Exec(ctx, q,
		item.UserID, item.Scope,
		nilIfEmpty(item.Title), nilIfEmpty(item.Author), nilIfEmpty(item.ISBN),
		item.SeriesID, nilIfEmpty(item.SeriesName),
	)
	if err != nil {
		return fmt.Errorf("insert block: %w", err)
	}
	return nil
}

// ListBlocks returns every block for a user. Used by the worker to render
// exclusions into the prompt.
func (r *AISuggestionsRepo) ListBlocks(ctx context.Context, userID uuid.UUID) ([]*models.AIBlockedItem, error) {
	const q = `
		SELECT id, user_id, scope,
			COALESCE(title,''), COALESCE(author,''), COALESCE(isbn,''),
			series_id, COALESCE(series_name,''), blocked_at
		FROM ai_blocked_items WHERE user_id = $1 ORDER BY blocked_at DESC`
	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list blocks: %w", err)
	}
	defer rows.Close()
	var out []*models.AIBlockedItem
	for rows.Next() {
		b := &models.AIBlockedItem{}
		if err := rows.Scan(&b.ID, &b.UserID, &b.Scope,
			&b.Title, &b.Author, &b.ISBN,
			&b.SeriesID, &b.SeriesName, &b.BlockedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ─── Steering hydration ──────────────────────────────────────────────────────

// NamedEntity is one hydrated {id, name} pair — used for author/series/genre
// names rendered into prompts and API responses for steered runs.
type NamedEntity struct {
	ID   uuid.UUID
	Name string
}

// NamedTag is a hydrated tag, including its owning library for UI grouping
// (tags are library-scoped, so two tags can share a name across libraries).
type NamedTag struct {
	ID        uuid.UUID
	Name      string
	LibraryID uuid.UUID
}

// HydratedSteering is a SuggestionSteering with display names filled in,
// ready to render into a prompt or return from a handler. An ID that no
// longer resolves (e.g. the author was deleted after the ask) is simply
// dropped — the ask survives in the JSONB column but we only surface what
// still exists.
type HydratedSteering struct {
	Authors []NamedEntity
	Series  []NamedEntity
	Genres  []NamedEntity
	Tags    []NamedTag
	Notes   string
}

// IsEmpty reports whether nothing at all resolved (every ID was stale and no
// notes were set). Callers can short-circuit rendering in that case.
func (h *HydratedSteering) IsEmpty() bool {
	return h == nil ||
		(len(h.Authors) == 0 && len(h.Series) == 0 && len(h.Genres) == 0 && len(h.Tags) == 0 && h.Notes == "")
}

// HydrateSteering looks up display names for every ID in the steering payload.
// Returns nil (no error) when the input is nil or entirely empty.
func (r *AISuggestionsRepo) HydrateSteering(ctx context.Context, s *models.SuggestionSteering) (*HydratedSteering, error) {
	if s == nil || s.IsEmpty() {
		return nil, nil
	}
	out := &HydratedSteering{Notes: s.Notes}
	if len(s.AuthorIDs) > 0 {
		named, err := r.resolveNamed(ctx, `SELECT id, name FROM contributors WHERE id = ANY($1)`, s.AuthorIDs)
		if err != nil {
			return nil, fmt.Errorf("hydrate author names: %w", err)
		}
		out.Authors = named
	}
	if len(s.SeriesIDs) > 0 {
		named, err := r.resolveNamed(ctx, `SELECT id, name FROM series WHERE id = ANY($1)`, s.SeriesIDs)
		if err != nil {
			return nil, fmt.Errorf("hydrate series names: %w", err)
		}
		out.Series = named
	}
	if len(s.GenreIDs) > 0 {
		named, err := r.resolveNamed(ctx, `SELECT id, name FROM genres WHERE id = ANY($1)`, s.GenreIDs)
		if err != nil {
			return nil, fmt.Errorf("hydrate genre names: %w", err)
		}
		out.Genres = named
	}
	if len(s.TagIDs) > 0 {
		rows, err := r.db.Query(ctx, `SELECT id, name, library_id FROM tags WHERE id = ANY($1)`, s.TagIDs)
		if err != nil {
			return nil, fmt.Errorf("hydrate tag names: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var t NamedTag
			if err := rows.Scan(&t.ID, &t.Name, &t.LibraryID); err != nil {
				return nil, err
			}
			out.Tags = append(out.Tags, t)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *AISuggestionsRepo) resolveNamed(ctx context.Context, query string, ids []uuid.UUID) ([]NamedEntity, error) {
	rows, err := r.db.Query(ctx, query, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NamedEntity
	for rows.Next() {
		var n NamedEntity
		if err := rows.Scan(&n.ID, &n.Name); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ResolveNames returns an id→name map for a single {id, name}-shaped table.
// table must be one of the known taxonomy tables — we gate it in code because
// Postgres can't parameterise identifiers. Unknown tables return an error.
// An empty ids slice returns an empty map with no query.
func (r *AISuggestionsRepo) ResolveNames(ctx context.Context, table string, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var query string
	switch table {
	case "contributors":
		query = `SELECT id, name FROM contributors WHERE id = ANY($1)`
	case "series":
		query = `SELECT id, name FROM series WHERE id = ANY($1)`
	case "genres":
		query = `SELECT id, name FROM genres WHERE id = ANY($1)`
	default:
		return nil, fmt.Errorf("resolve names: unsupported table %q", table)
	}
	rows, err := r.db.Query(ctx, query, ids)
	if err != nil {
		return nil, fmt.Errorf("resolve %s names: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// ResolveTags returns an id→tag map; tags need their owning library alongside
// the name because the UI groups tags per library.
func (r *AISuggestionsRepo) ResolveTags(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]NamedTag, error) {
	out := make(map[uuid.UUID]NamedTag, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `SELECT id, name, library_id FROM tags WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("resolve tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t NamedTag
		if err := rows.Scan(&t.ID, &t.Name, &t.LibraryID); err != nil {
			return nil, err
		}
		out[t.ID] = t
	}
	return out, rows.Err()
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func splitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}
