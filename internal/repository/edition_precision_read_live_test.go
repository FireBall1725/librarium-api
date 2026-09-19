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

// An edition read back carries its date precision, so a form can show "1965"
// instead of the 1 January it's stored as.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Everything runs in a rolled-back
// transaction.
func TestEditionReadCarriesPrecision(t *testing.T) {
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
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	bookID, editionID := uuid.New(), uuid.New()
	title := "precision read fixture " + bookID.String()
	if _, err := tx.Exec(ctx, `INSERT INTO books (id, title, media_type_id, sort_title, title_key) VALUES ($1, $2, $3, $4, $5)`,
		bookID, title, mediaTypeID, title, title); err != nil {
		t.Fatalf("creating book: %v", err)
	}
	year := time.Date(1965, 1, 1, 0, 0, 0, 0, time.UTC)
	repo := &EditionRepo{db: pool}
	if err := repo.Create(ctx, tx, editionID, bookID, "paperback", "en", "", "", "", &year, models.DatePrecisionYear,
		"", "", "", nil, nil, false, nil); err != nil {
		t.Fatalf("create: %v", err)
	}

	e, err := scanEdition(tx.QueryRow(ctx, `SELECT `+editionColumns+` FROM book_editions WHERE id = $1`, editionID))
	if err != nil {
		t.Fatalf("scanning: %v", err)
	}
	if e.PublishDatePrecision != models.DatePrecisionYear {
		t.Errorf("precision = %q, want year", e.PublishDatePrecision)
	}
}
