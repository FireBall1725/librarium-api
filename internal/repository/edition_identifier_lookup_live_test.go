// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestFindByIdentifierInLibrary is how a scanned UPC finds a comic already on
// the shelf: the code lives only in edition_identifiers, and it only counts in
// a library that holds a copy.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Creates its own book and removes it.
func TestFindByIdentifierInLibrary(t *testing.T) {
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

	var mediaTypeID, libraryID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types ORDER BY name LIMIT 1`).Scan(&mediaTypeID); err != nil {
		t.Skipf("no media types: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries ORDER BY created_at LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no libraries: %v", err)
	}

	bookID, editionID := uuid.New(), uuid.New()
	title := "upc fixture " + bookID.String()
	// A UPC nobody else can hold: the value is the primary key with its scheme.
	upc := "0" + bookID.String()[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO books (id, title, media_type_id, sort_title, title_key)
		VALUES ($1, $2, $3, $4, $5)`, bookID, title, mediaTypeID, title, title); err != nil {
		t.Fatalf("creating fixture book: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM copies WHERE book_id = $1`, bookID)
		_, _ = pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, bookID)
	}()
	if _, err := pool.Exec(ctx, `INSERT INTO book_editions (id, book_id, format, is_primary) VALUES ($1, $2, 'paperback', true)`, editionID, bookID); err != nil {
		t.Fatalf("creating fixture edition: %v", err)
	}
	// Saved the way a book added from a scan saves it, duplicates and all.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := AddIdentifiersInTx(ctx, tx, editionID, []models.EditionIdentifierInput{
		{Scheme: "UPC", Value: upc}, {Scheme: "upc", Value: " " + upc}, {Scheme: "ean", Value: ""},
	}); err != nil {
		t.Fatalf("adding fixture upc: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// A scheme the server doesn't know is refused, not stored.
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := AddIdentifiersInTx(ctx, tx, editionID, []models.EditionIdentifierInput{{Scheme: "nope", Value: upc}}); !errors.Is(err, ErrUnknownScheme) {
		t.Fatalf("unknown scheme: want ErrUnknownScheme, got %v", err)
	}
	_ = tx.Rollback(ctx)

	repo := &EditionRepo{db: pool}
	if got, err := repo.FindByIdentifier(ctx, "upc", upc); err != nil || got.ID != editionID {
		t.Fatalf("global lookup: got %v, %v", got, err)
	}

	// Not held yet, so not found even though the identifier exists.
	if _, err := repo.FindByIdentifierInLibrary(ctx, libraryID, "upc", upc); !errors.Is(err, ErrNotFound) {
		t.Fatalf("before a copy exists: want ErrNotFound, got %v", err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO copies (library_id, book_id, edition_id) VALUES ($1, $2, $3)`, libraryID, bookID, editionID); err != nil {
		t.Fatalf("adding fixture copy: %v", err)
	}

	got, err := repo.FindByIdentifierInLibrary(ctx, libraryID, "UPC", " "+upc+" ")
	if err != nil {
		t.Fatalf("held copy: %v", err)
	}
	if got.ID != editionID {
		t.Fatalf("want edition %s, got %s", editionID, got.ID)
	}

	if _, err := repo.FindByIdentifierInLibrary(ctx, uuid.New(), "upc", upc); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another library: want ErrNotFound, got %v", err)
	}
	if _, err := repo.FindByIdentifierInLibrary(ctx, libraryID, "ean", upc); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong scheme: want ErrNotFound, got %v", err)
	}
}
