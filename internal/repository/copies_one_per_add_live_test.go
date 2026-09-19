// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Adding a book used to leave two copies: the editionless one AddBookToLibrary
// records, and a second for the edition (librarium-ios #81). The edition now
// goes to the first.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Everything runs in a rolled-back
// transaction.
func TestOneCopyPerAdd(t *testing.T) {
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

	// fixture makes a book with one edition, inside tx.
	fixture := func(t *testing.T, tx pgx.Tx) (bookID, editionID uuid.UUID) {
		t.Helper()
		bookID, editionID = uuid.New(), uuid.New()
		title := "copies fixture " + bookID.String()
		if _, err := tx.Exec(ctx, `INSERT INTO books (id, title, media_type_id, sort_title, title_key) VALUES ($1, $2, $3, $4, $5)`,
			bookID, title, mediaTypeID, title, title); err != nil {
			t.Fatalf("creating book: %v", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO book_editions (id, book_id, format, is_primary) VALUES ($1, $2, 'paperback', true)`, editionID, bookID); err != nil {
			t.Fatalf("creating edition: %v", err)
		}
		return
	}
	// copies counts a book's live copies in the library, and how many have an edition.
	copies := func(t *testing.T, tx pgx.Tx, bookID uuid.UUID) (total, withEdition int) {
		t.Helper()
		if err := tx.QueryRow(ctx, `SELECT count(*), count(edition_id) FROM copies WHERE library_id = $1 AND book_id = $2 AND deleted_at IS NULL`,
			libraryID, bookID).Scan(&total, &withEdition); err != nil {
			t.Fatalf("counting copies: %v", err)
		}
		return
	}
	begin := func(t *testing.T) pgx.Tx {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		t.Cleanup(func() { _ = tx.Rollback(ctx) })
		return tx
	}

	libraryBooks := &LibraryBookRepo{db: pool}

	t.Run("new book with an edition", func(t *testing.T) {
		tx := begin(t)
		bookID, editionID := fixture(t, tx)
		if err := libraryBooks.AddBookToLibrary(ctx, tx, libraryID, bookID, nil); err != nil {
			t.Fatal(err)
		}
		if err := libraryBooks.SetEditionCopyCount(ctx, tx, libraryID, editionID, 1, nil); err != nil {
			t.Fatal(err)
		}
		if total, with := copies(t, tx, bookID); total != 1 || with != 1 {
			t.Errorf("got %d copies, %d with the edition; want 1 and 1", total, with)
		}
	})

	t.Run("existing edition added to a library", func(t *testing.T) {
		// IncrementCopyCount runs on the pool, so this case commits and cleans up.
		bookID, editionID := uuid.New(), uuid.New()
		title := "copies fixture " + bookID.String()
		if _, err := pool.Exec(ctx, `INSERT INTO books (id, title, media_type_id, sort_title, title_key) VALUES ($1, $2, $3, $4, $5)`,
			bookID, title, mediaTypeID, title, title); err != nil {
			t.Fatalf("creating book: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM copies WHERE book_id = $1`, bookID)
			_, _ = pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, bookID)
		})
		if _, err := pool.Exec(ctx, `INSERT INTO book_editions (id, book_id, format, is_primary) VALUES ($1, $2, 'paperback', true)`, editionID, bookID); err != nil {
			t.Fatalf("creating edition: %v", err)
		}
		editions := &EditionRepo{db: pool}
		count := func() (total, with int) {
			if err := pool.QueryRow(ctx, `SELECT count(*), count(edition_id) FROM copies WHERE library_id = $1 AND book_id = $2 AND deleted_at IS NULL`,
				libraryID, bookID).Scan(&total, &with); err != nil {
				t.Fatalf("counting copies: %v", err)
			}
			return
		}

		if err := libraryBooks.AddBookToLibrary(ctx, nil, libraryID, bookID, nil); err != nil {
			t.Fatal(err)
		}
		if err := editions.IncrementCopyCount(ctx, libraryID, editionID); err != nil {
			t.Fatal(err)
		}
		if total, with := count(); total != 1 || with != 1 {
			t.Errorf("after one add: %d copies, %d with the edition; want 1 and 1", total, with)
		}

		// Adding it again on purpose is a second copy.
		if err := libraryBooks.AddBookToLibrary(ctx, nil, libraryID, bookID, nil); err != nil {
			t.Fatal(err)
		}
		if err := editions.IncrementCopyCount(ctx, libraryID, editionID); err != nil {
			t.Fatal(err)
		}
		if total, with := count(); total != 2 || with != 2 {
			t.Errorf("after adding again: %d copies, %d with the edition; want 2 and 2", total, with)
		}
	})

	t.Run("migration 41 removes the extra copy and nothing typed", func(t *testing.T) {
		tx := begin(t)
		up, err := os.ReadFile("../db/migrations/000041_one_copy_per_add.up.sql")
		if err != nil {
			t.Fatal(err)
		}
		// The shape the bug left: an editionless copy beside an edition copy.
		doubled, editionID := fixture(t, tx)
		if _, err := tx.Exec(ctx, `INSERT INTO copies (library_id, book_id) VALUES ($1, $2)`, libraryID, doubled); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO copies (library_id, book_id, edition_id) VALUES ($1, $2, $3)`, libraryID, doubled, editionID); err != nil {
			t.Fatal(err)
		}
		// Same shape, but someone wrote a note on the editionless copy: kept.
		noted, notedEdition := fixture(t, tx)
		if _, err := tx.Exec(ctx, `INSERT INTO copies (library_id, book_id, notes) VALUES ($1, $2, 'gift from Mum')`, libraryID, noted); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO copies (library_id, book_id, edition_id) VALUES ($1, $2, $3)`, libraryID, noted, notedEdition); err != nil {
			t.Fatal(err)
		}
		// Run the migration's body; drop its bookkeeping table first in case
		// the database already applied 41.
		if _, err := tx.Exec(ctx, `DROP TABLE IF EXISTS copies_removed_by_41`); err != nil {
			t.Fatal(err)
		}
		for _, stmt := range strings.Split(string(up), ";") {
			if strings.TrimSpace(stripSQLComments(stmt)) == "" {
				continue
			}
			if _, err := tx.Exec(ctx, stmt); err != nil {
				t.Fatalf("running migration: %v", err)
			}
		}
		if total, with := copies(t, tx, doubled); total != 1 || with != 1 {
			t.Errorf("doubled book: %d copies, %d with the edition; want 1 and 1", total, with)
		}
		if total, _ := copies(t, tx, noted); total != 2 {
			t.Errorf("a copy with a note was removed: %d copies left, want 2", total)
		}
	})
}

func stripSQLComments(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
