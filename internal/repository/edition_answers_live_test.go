// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/fireball1725/librarium-api/internal/providers"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Asking a provider again replaces its own answer and leaves the others.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Creates its own book and removes it.
func TestEditionAnswersSaveAndReplace(t *testing.T) {
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
	bookID, editionID := uuid.New(), uuid.New()
	title := "answers fixture " + bookID.String()
	if _, err := pool.Exec(ctx, `INSERT INTO books (id, title, media_type_id, sort_title, title_key) VALUES ($1, $2, $3, $4, $5)`,
		bookID, title, mediaTypeID, title, title); err != nil {
		t.Fatalf("creating book: %v", err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, bookID) }()
	if _, err := pool.Exec(ctx, `INSERT INTO book_editions (id, book_id, format, is_primary) VALUES ($1, $2, 'paperback', true)`, editionID, bookID); err != nil {
		t.Fatalf("creating edition: %v", err)
	}

	repo := NewEditionAnswerRepo(pool)
	if got, err := repo.ListForEdition(ctx, editionID); err != nil || len(got) != 0 {
		t.Fatalf("new edition: %v, %v", got, err)
	}
	if err := repo.Save(ctx, editionID, "9780441172719", []*providers.BookResult{
		{Provider: "open_library", Title: "Dune"},
		{Provider: "hardcover", Title: "Dune"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := repo.Save(ctx, editionID, "9780441172719", []*providers.BookResult{{Provider: "open_library", Title: "Dune (Ace)"}}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	got, err := repo.ListForEdition(ctx, editionID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	titles := map[string]string{}
	for _, a := range got {
		titles[a.Provider] = a.Result.Title
	}
	if len(got) != 2 || titles["open_library"] != "Dune (Ace)" || titles["hardcover"] != "Dune" {
		t.Errorf("answers = %v", titles)
	}
}
