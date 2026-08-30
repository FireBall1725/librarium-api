// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestEditionPublishPrecisionRoundTrip covers the constraint migration 000025
// added: editions_precision_needs_date requires publish_date and
// publish_date_precision to be NULL together. Every write path used to set the
// date and never the precision, so adding a book with a publication date failed
// with SQLSTATE 23514 and nothing was saved.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Creates its own book and removes it.
func TestEditionPublishPrecisionRoundTrip(t *testing.T) {
	dsn := os.Getenv("LIBRARIUM_TEST_DSN")
	if dsn == "" {
		t.Skip("set LIBRARIUM_TEST_DSN to run")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer pool.Close()

	var mediaTypeID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types ORDER BY name LIMIT 1`).Scan(&mediaTypeID); err != nil {
		t.Skipf("no media types: %v", err)
	}

	bookID := uuid.New()
	title := "precision fixture " + bookID.String()
	if _, err := pool.Exec(ctx, `
		INSERT INTO books (id, title, media_type_id, sort_title, title_key)
		VALUES ($1, $2, $3, $4, $5)`, bookID, title, mediaTypeID, title, title); err != nil {
		t.Fatalf("creating fixture book: %v", err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, bookID) }()

	repo := &EditionRepo{db: pool}
	day := time.Date(1932, 5, 14, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		date      any
		precision models.DatePrecision
		want      any
	}{
		{name: "day", date: &day, precision: models.DatePrecisionDay, want: "day"},
		{name: "month", date: &day, precision: models.DatePrecisionMonth, want: "month"},
		{name: "year", date: &day, precision: models.DatePrecisionYear, want: "year"},
		// A caller that has a date but never established how specific it is.
		{name: "unstated", date: &day, precision: "", want: "day"},
		// No date means no precision, which is the other half of the constraint.
		{name: "no date", date: nil, precision: "", want: nil},
		// A precision with no date must not be written, or the row is rejected.
		{name: "precision without a date", date: nil, precision: models.DatePrecisionYear, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(ctx) }()

			editionID := uuid.New()
			if err := repo.Create(ctx, tx, editionID, bookID,
				"paperback", "en", "", "", "", tt.date, tt.precision,
				"", "", "", nil, nil, false, nil); err != nil {
				t.Fatalf("create: %v", err)
			}

			var got *string
			if err := tx.QueryRow(ctx,
				`SELECT publish_date_precision FROM book_editions WHERE id = $1`, editionID).Scan(&got); err != nil {
				t.Fatalf("reading back: %v", err)
			}
			assertPrecision(t, "create", got, tt.want)

			// Update has to keep the pair consistent too: clearing a date on a
			// row that had one must clear the precision with it.
			if err := repo.Update(ctx, tx, editionID,
				"paperback", "en", "", "", "", nil, tt.precision,
				"", "", "", nil, nil, false, nil); err != nil {
				t.Fatalf("update clearing the date: %v", err)
			}
			if err := tx.QueryRow(ctx,
				`SELECT publish_date_precision FROM book_editions WHERE id = $1`, editionID).Scan(&got); err != nil {
				t.Fatalf("reading back after update: %v", err)
			}
			assertPrecision(t, "update", got, nil)
		})
	}
}

func assertPrecision(t *testing.T, stage string, got *string, want any) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("%s: precision = %q, want NULL", stage, *got)
		}
		return
	}
	if got == nil {
		t.Errorf("%s: precision = NULL, want %q", stage, want)
		return
	}
	if *got != want.(string) {
		t.Errorf("%s: precision = %q, want %q", stage, *got, want)
	}
}
