// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Add Book files the new copy on the shelf in the destination bar, and hands
// over authors by name so it needn't search and create each one itself.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Makes its own library, places,
// book and contributors, and removes them.
func TestCreateBookFilesTheCopyAndResolvesNames(t *testing.T) {
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

	run := uuid.NewString()[:8]
	var user, mediaType uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types ORDER BY name LIMIT 1`).Scan(&mediaType); err != nil {
		t.Skipf("no media types: %v", err)
	}
	user = uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, username, display_name, email) VALUES ($1, $2, $3, $4)`,
		user, "addbooks-"+run, "addbooks fixture", run+"@example.test"); err != nil {
		t.Fatal(err)
	}
	lib, other := uuid.New(), uuid.New()
	for _, l := range []uuid.UUID{lib, other} {
		if _, err := pool.Exec(ctx, `INSERT INTO libraries (id, name, slug, owner_id) VALUES ($1, $2, $3, $4)`,
			l, "addbooks fixture "+l.String(), "addbooks-"+l.String(), user); err != nil {
			t.Fatal(err)
		}
	}
	author := "Addbooks Author " + run
	existing := "Addbooks Existing " + run
	var bookID uuid.UUID
	t.Cleanup(func() {
		for _, step := range []struct {
			q    string
			args []any
		}{
			{`DELETE FROM copies WHERE library_id = ANY($1)`, []any{[]uuid.UUID{lib, other}}},
			{`DELETE FROM books WHERE id = $1`, []any{bookID}},
			{`DELETE FROM contributors WHERE name = ANY($1)`, []any{[]string{author, existing}}},
			{`DELETE FROM copy_locations WHERE library_id = ANY($1)`, []any{[]uuid.UUID{lib, other}}},
			{`DELETE FROM libraries WHERE id = ANY($1)`, []any{[]uuid.UUID{lib, other}}},
			{`DELETE FROM users WHERE id = $1`, []any{user}},
		} {
			if _, err := pool.Exec(ctx, step.q, step.args...); err != nil {
				t.Logf("cleanup: %v", err)
			}
		}
	})

	locations := repository.NewCopyLocationRepo(pool)
	shelf, err := locations.Create(ctx, lib, "Shelf 3", nil)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := locations.Create(ctx, other, "Somewhere else", nil)
	if err != nil {
		t.Fatal(err)
	}
	contributors := repository.NewContributorRepo(pool)
	pre, err := contributors.Create(ctx, uuid.New(), existing, DeriveSortName(existing), false)
	if err != nil {
		t.Fatal(err)
	}

	svc := NewBookService(pool, repository.NewBookRepo(pool), repository.NewLibraryBookRepo(pool), contributors,
		repository.NewEditionRepo(pool), repository.NewTagRepo(pool), repository.NewGenreRepo(pool),
		repository.NewCoverRepo(pool), repository.NewAISuggestionsRepo(pool), "")
	svc.SetLocationRepo(locations)

	isbn := isbn13("9798" + fmt.Sprintf("%08d", uuid.New().ID()%100000000))
	req := BookRequest{
		Title: "Addbooks fixture " + run, MediaTypeID: mediaType,
		NamedContributors: []NamedContributor{{Name: author}, {Name: existing, Role: "illustrator"}, {Name: author}},
		Edition:           &EditionRequest{Format: "paperback", ISBN13: isbn, IsPrimary: true},
		LocationID:        &shelf.ID,
	}
	book, err := svc.CreateBook(ctx, lib, user, req)
	if err != nil {
		t.Fatal(err)
	}
	bookID = book.ID

	var filed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM copies WHERE book_id = $1 AND library_id = $2 AND location_id = $3 AND deleted_at IS NULL`,
		book.ID, lib, shelf.ID).Scan(&filed); err != nil || filed != 1 {
		t.Errorf("new book: %d copies on the shelf (%v), want 1", filed, err)
	}
	var names []string
	var reusedPre bool
	rows, _ := pool.Query(ctx, `SELECT c.name, c.id = $2 FROM book_contributors bc JOIN contributors c ON c.id = bc.contributor_id WHERE bc.book_id = $1 ORDER BY bc.display_order`, book.ID, pre.ID)
	for rows.Next() {
		var n string
		var isPre bool
		_ = rows.Scan(&n, &isPre)
		names = append(names, n)
		reusedPre = reusedPre || isPre
	}
	rows.Close()
	if len(names) != 2 || names[0] != author || !reusedPre {
		t.Errorf("contributors = %v (existing reused: %v), want the new author then the existing person, once each", names, reusedPre)
	}

	// The same ISBN again is a second copy of that edition, filed too.
	if _, err := svc.CreateBook(ctx, lib, user, req); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM copies WHERE book_id = $1 AND library_id = $2 AND location_id = $3 AND deleted_at IS NULL`,
		book.ID, lib, shelf.ID).Scan(&filed); err != nil || filed != 2 {
		t.Errorf("second copy: %d copies on the shelf (%v), want 2", filed, err)
	}

	// A place in another library is refused before anything is written.
	req.LocationID = &elsewhere.ID
	if _, err := svc.CreateBook(ctx, lib, user, req); !errors.Is(err, ErrLocationElsewhere) {
		t.Errorf("place in another library: %v, want ErrLocationElsewhere", err)
	}
}

// isbn13 adds the check digit to twelve digits.
func isbn13(body string) string {
	sum := 0
	for i, c := range body {
		d := int(c - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return body + fmt.Sprint((10-sum%10)%10)
}
