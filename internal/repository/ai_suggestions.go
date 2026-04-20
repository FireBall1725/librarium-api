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

// CreateRun inserts a run row in 'running' state and returns its ID.
func (r *AISuggestionsRepo) CreateRun(ctx context.Context, userID uuid.UUID, triggeredBy, providerType, modelID string) (uuid.UUID, error) {
	const q = `
		INSERT INTO ai_suggestion_runs (user_id, triggered_by, provider_type, model_id, status)
		VALUES ($1, $2, $3, $4, 'running') RETURNING id`
	var id uuid.UUID
	if err := r.db.QueryRow(ctx, q, userID, triggeredBy, providerType, modelID).Scan(&id); err != nil {
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

// RunsInLast24h counts completed or running suggestion runs for a user over
// the last 24h — used to enforce the per-user-run rate limit.
func (r *AISuggestionsRepo) RunsInLast24h(ctx context.Context, userID uuid.UUID) (int, error) {
	const q = `
		SELECT COUNT(*) FROM ai_suggestion_runs
		WHERE user_id = $1 AND started_at >= NOW() - INTERVAL '24 hours'`
	var n int
	if err := r.db.QueryRow(ctx, q, userID).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
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
// inserting, the oldest 'new' rows beyond maxPerUser are evicted so the user
// never sees an unbounded backlog. Pass 0 to disable eviction.
//
// Dismissed/interested/added rows from any run are preserved — they're filtered
// out of the user view by status, not by deletion.
func (r *AISuggestionsRepo) AppendSuggestions(ctx context.Context, userID, runID uuid.UUID, items []SuggestionInput, maxPerUser int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	const ins = `
		INSERT INTO ai_suggestions (user_id, run_id, type, book_id, book_edition_id,
			title, author, isbn, cover_url, reasoning, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'new')
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

	if maxPerUser > 0 {
		const evict = `
			DELETE FROM ai_suggestions
			WHERE id IN (
				SELECT id FROM ai_suggestions
				WHERE user_id = $1 AND status = 'new'
				ORDER BY created_at DESC
				OFFSET $2
			)`
		if _, err := tx.Exec(ctx, evict, userID, maxPerUser); err != nil {
			return fmt.Errorf("evict oldest: %w", err)
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
// ('buy' | 'read_next' | '' for all) and by status ('new' | '' for all).
func (r *AISuggestionsRepo) ListSuggestions(ctx context.Context, userID uuid.UUID, typeFilter, statusFilter string) ([]*models.AISuggestionWithLibrary, error) {
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
	if statusFilter != "" {
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
		       started_at, finished_at
		FROM ai_suggestion_runs WHERE id = $1`
	run := &models.AISuggestionRun{}
	err := r.db.QueryRow(ctx, q, runID).Scan(
		&run.ID, &run.UserID, &run.TriggeredBy, &run.ProviderType, &run.ModelID,
		&run.Status, &run.Error, &run.TokensIn, &run.TokensOut, &run.EstimatedCostUSD,
		&run.StartedAt, &run.FinishedAt,
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
		       started_at, finished_at
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
		       started_at, finished_at
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
			&run.StartedAt, &run.FinishedAt,
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
