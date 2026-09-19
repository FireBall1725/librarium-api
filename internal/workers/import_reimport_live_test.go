// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package workers

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestReimportNeverBlanksReadingData pins the fix in 8813aa9: re-importing a
// CSV used to overwrite every interaction column, so a sparse row wiped a
// hand-written review. A later row only fills in what it carries, and a row
// with nothing interaction-shaped doesn't write at all.
//
// Rows go through applyInteraction, the path an import runs, with every
// column present the way a real CSV has them. Skipped unless
// LIBRARIUM_TEST_DSN is set. Creates its own user, book and edition and
// removes them.
func TestReimportNeverBlanksReadingData(t *testing.T) {
	dsn := os.Getenv("LIBRARIUM_TEST_DSN")
	if dsn == "" {
		t.Skip("set LIBRARIUM_TEST_DSN to run")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close) // not defer: defers run before cleanups

	userID, editionID := seedReader(ctx, t, pool)
	w := &ImportWorker{editions: repository.NewEditionRepo(pool)}

	row := func(readStatus, rating, review, finished, favorite string) map[string]string {
		return map[string]string{
			"read_status": readStatus, "rating": rating, "review": review, "notes": "",
			"date_started": "", "date_finished": finished, "is_favorite": favorite,
			"pages_read": "", "progress_percent": "", "progress_position": "",
		}
	}
	type state struct {
		status   string
		rating   *int
		review   string
		favorite bool
		finished *time.Time
		updated  time.Time
	}
	read := func() state {
		t.Helper()
		var s state
		if err := pool.QueryRow(ctx, `
			SELECT ub.read_status, ub.rating, ub.review, ub.is_favorite, ub.updated_at,
			       (SELECT max(rs.finished_at) FROM reading_sessions rs
			         WHERE rs.user_id = ub.user_id AND rs.book_id = ub.book_id)
			  FROM user_books ub JOIN book_editions e ON e.book_id = ub.book_id
			 WHERE ub.user_id = $1 AND e.id = $2`, userID, editionID).
			Scan(&s.status, &s.rating, &s.review, &s.favorite, &s.updated, &s.finished); err != nil {
			t.Fatalf("reading back: %v", err)
		}
		return s
	}
	finish := time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC)

	// Rating 8 is four stars on the CSV's five-star scale.
	w.applyInteraction(ctx, editionID, userID, row("reading", "4", "Slow start, great ending.", "2026-03-14", "true"))
	full := read()
	if full.status != "reading" || full.rating == nil || *full.rating != 8 ||
		full.review != "Slow start, great ending." || !full.favorite ||
		full.finished == nil || !full.finished.Equal(finish) {
		t.Fatalf("first import didn't write the full row: %+v", full)
	}

	// Only a rating this time (4.5 stars is 9): everything else survives.
	w.applyInteraction(ctx, editionID, userID, row("", "4.5", "", "", ""))
	merged := read()
	if merged.rating == nil || *merged.rating != 9 {
		t.Errorf("rating = %v, want 9", merged.rating)
	}
	if merged.status != "reading" {
		t.Errorf("read status blanked: %q, want reading", merged.status)
	}
	if merged.review != full.review {
		t.Errorf("review blanked: %q", merged.review)
	}
	if !merged.favorite {
		t.Error("favourite blanked by a row with an empty is_favorite column")
	}
	if merged.finished == nil || !merged.finished.Equal(finish) {
		t.Errorf("finish date blanked: %v, want %v", merged.finished, finish)
	}

	// Nothing interaction-shaped: no write, so updated_at stays put.
	w.applyInteraction(ctx, editionID, userID, row("", "", "", "", ""))
	if blank := read(); !blank.updated.Equal(merged.updated) {
		t.Errorf("an empty row wrote to user_books: updated_at %v, was %v", blank.updated, merged.updated)
	}
}

// seedReader makes a user and a one-edition book, and registers cleanup that
// removes only those rows and then checks nothing of them is left.
func seedReader(ctx context.Context, t *testing.T, pool *pgxpool.Pool) (userID, editionID uuid.UUID) {
	t.Helper()

	var mediaTypeID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types ORDER BY name LIMIT 1`).Scan(&mediaTypeID); err != nil {
		t.Fatalf("no media types seeded: %v", err)
	}
	userID, bookID, editionID := uuid.New(), uuid.New(), uuid.New()

	// Registered first so it runs last, after the deletes below.
	t.Cleanup(func() {
		var left int
		if err := pool.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM users WHERE id = $1)
			     + (SELECT count(*) FROM books WHERE id = $2)
			     + (SELECT count(*) FROM book_editions WHERE id = $3)
			     + (SELECT count(*) FROM user_books WHERE user_id = $1 OR book_id = $2)
			     + (SELECT count(*) FROM reading_sessions WHERE user_id = $1 OR book_id = $2)`,
			userID, bookID, editionID).Scan(&left); err != nil {
			t.Errorf("checking cleanup: %v", err)
		} else if left != 0 {
			t.Errorf("cleanup left %d fixture rows behind", left)
		}
	})
	t.Cleanup(func() {
		for _, del := range []struct {
			q  string
			id uuid.UUID
		}{
			{`DELETE FROM reading_sessions WHERE user_id = $1`, userID},
			{`DELETE FROM user_books WHERE user_id = $1`, userID},
			{`DELETE FROM books WHERE id = $1`, bookID}, // cascades to the edition
			{`DELETE FROM users WHERE id = $1`, userID},
		} {
			if _, err := pool.Exec(ctx, del.q, del.id); err != nil {
				t.Logf("cleanup: %v", err)
			}
		}
	})

	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email)
		VALUES ($1, $2, 'reimport fixture', $3)`,
		userID, "reimport-"+userID.String(), userID.String()+"@example.test"); err != nil {
		t.Fatalf("creating user: %v", err)
	}
	title := "reimport fixture " + bookID.String()
	if _, err := pool.Exec(ctx, `
		INSERT INTO books (id, title, media_type_id, sort_title, title_key)
		VALUES ($1, $2, $3, $4, $5)`, bookID, title, mediaTypeID, title, title); err != nil {
		t.Fatalf("creating book: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO book_editions (id, book_id, format, is_primary) VALUES ($1, $2, 'paperback', true)`,
		editionID, bookID); err != nil {
		t.Fatalf("creating edition: %v", err)
	}
	return userID, editionID
}
