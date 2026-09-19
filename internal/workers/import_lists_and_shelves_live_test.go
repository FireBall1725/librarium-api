// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package workers

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestResolveListFindsOrCreates covers the half of the list column that can
// go wrong quietly: a name that already exists must reuse that list rather
// than create a second one with the same name, and the match is
// case-insensitive because a spreadsheet is typed by hand.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Creates its own library and
// removes it.
func TestResolveListFindsOrCreates(t *testing.T) {
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

	userID, libraryID := seedLibrary(ctx, t, pool)
	w := &ImportWorker{lists: repository.NewShelfRepo(pool)}
	cache := map[string]uuid.UUID{}

	first, err := w.resolveList(ctx, libraryID, userID, "To Read", cache)
	if err != nil {
		t.Fatalf("creating a new shelf: %v", err)
	}

	// A second row naming the same shelf must land on the same one. The cache
	// answers this one, which is the common case in a large import.
	again, err := w.resolveList(ctx, libraryID, userID, "To Read", cache)
	if err != nil {
		t.Fatalf("resolving an existing shelf: %v", err)
	}
	if again != first {
		t.Errorf("same name resolved to a second shelf: %s then %s", first, again)
	}

	// Different case, and a cold cache, which is the path that has to fall
	// through to the list-and-match branch.
	cold, err := w.resolveList(ctx, libraryID, userID, "to read", map[string]uuid.UUID{})
	if err != nil {
		t.Fatalf("resolving with a cold cache: %v", err)
	}
	if cold != first {
		t.Errorf("case-insensitive match failed: %s vs %s", cold, first)
	}

	var shelves int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM lists
		 WHERE shared_library_id = $1 AND kind = 'manual' AND visibility = 'library'`,
		libraryID).Scan(&shelves); err != nil {
		t.Fatalf("counting shelves: %v", err)
	}
	if shelves != 1 {
		t.Errorf("library holds %d shelves, want 1", shelves)
	}
}

// TestAddBookToListIsAdditiveAndRollsBack covers the transaction boundary. An
// import row that fails after the shelf line must leave no membership behind,
// and a book already on the shelf must not error on a re-import.
func TestAddBookToListIsAdditiveAndRollsBack(t *testing.T) {
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

	userID, libraryID := seedLibrary(ctx, t, pool)
	shelves := repository.NewShelfRepo(pool)

	shelf, err := shelves.Create(ctx, uuid.New(), libraryID, "Landing", "", "", "", 0, userID)
	if err != nil {
		t.Fatalf("creating shelf: %v", err)
	}
	bookID := seedBook(ctx, t, pool, "shelf fixture")

	count := func() int {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM list_books WHERE list_id = $1`, shelf.ID).Scan(&n); err != nil {
			t.Fatalf("counting membership: %v", err)
		}
		return n
	}

	// Rolled back: nothing survives.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := shelves.AddBookTx(ctx, tx, shelf.ID, bookID); err != nil {
		t.Fatalf("adding inside a transaction: %v", err)
	}
	_ = tx.Rollback(ctx)
	if n := count(); n != 0 {
		t.Errorf("rolled-back membership survived: %d rows", n)
	}

	// Committed twice: still one row, no error the second time.
	for i := 0; i < 2; i++ {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if err := shelves.AddBookTx(ctx, tx, shelf.ID, bookID); err != nil {
			t.Fatalf("adding on pass %d: %v", i+1, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit on pass %d: %v", i+1, err)
		}
	}
	if n := count(); n != 1 {
		t.Errorf("membership rows = %d, want 1", n)
	}
}

// TestListColumnSplitsLikeTags pins the parsing the CSV column relies on, so
// "Sci-Fi, To Read" is two lists and stray whitespace is not a third.
func TestListColumnSplitsLikeTags(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"To Read", []string{"To Read"}},
		{"Sci-Fi, To Read", []string{"Sci-Fi", "To Read"}},
		{" Sci-Fi ,  To Read ", []string{"Sci-Fi", "To Read"}},
		{"Sci-Fi,,To Read", []string{"Sci-Fi", "To Read"}},
		{"", nil},
		{"  ,  ", nil},
	}

	for _, tt := range tests {
		var got []string
		if tt.in != "" {
			for _, raw := range strings.Split(tt.in, ",") {
				if name := strings.TrimSpace(raw); name != "" {
					got = append(got, name)
				}
			}
		}
		if len(got) != len(tt.want) {
			t.Errorf("%q split to %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range tt.want {
			if got[i] != tt.want[i] {
				t.Errorf("%q split to %v, want %v", tt.in, got, tt.want)
				break
			}
		}
	}
}

// A shelf path is a place on the Shelves page, found or created one level at a
// time. The same name under a different parent is a different place.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Creates its own library and
// removes it.
func TestResolvePlaceNests(t *testing.T) {
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

	_, libraryID := seedLibrary(ctx, t, pool)
	locations := repository.NewCopyLocationRepo(pool)
	w := &ImportWorker{locations: locations}

	top, err := w.resolvePlace(ctx, libraryID, "Office > Bookcase 2 > Top shelf", map[string]uuid.UUID{})
	if err != nil {
		t.Fatalf("creating a path: %v", err)
	}
	// Cold cache, different case and spacing: the same three places.
	again, err := w.resolvePlace(ctx, libraryID, "office>bookcase 2 >  TOP SHELF", map[string]uuid.UUID{})
	if err != nil || again != top {
		t.Fatalf("same path resolved to %s (err %v), want %s", again, err, top)
	}
	// "Top shelf" under Office rather than under Bookcase 2 is another place.
	other, err := w.resolvePlace(ctx, libraryID, "Office > Top shelf", map[string]uuid.UUID{})
	if err != nil || other == top {
		t.Fatalf("a same-named place under another parent was reused: %s (err %v)", other, err)
	}

	all, err := locations.List(ctx, libraryID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		names := make([]string, len(all))
		for i, l := range all {
			names[i] = l.Name
		}
		t.Errorf("want 4 places (Office, Bookcase 2, two Top shelf), got %v", names)
	}
	if _, err := w.resolvePlace(ctx, libraryID, " > ", map[string]uuid.UUID{}); err == nil {
		t.Error("an empty path should be an error, not a nameless place")
	}
}

func seedLibrary(ctx context.Context, t *testing.T, pool *pgxpool.Pool) (userID, libraryID uuid.UUID) {
	t.Helper()

	userID = uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email)
		VALUES ($1, $2, 'import fixture', $3)`,
		userID, "import-"+userID.String(), userID.String()+"@example.test"); err != nil {
		t.Fatalf("creating user: %v", err)
	}

	libraryID = uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO libraries (id, name, slug, owner_id) VALUES ($1, $2, $3, $4)`,
		libraryID, "import fixture "+libraryID.String(),
		"import-fixture-"+libraryID.String(), userID); err != nil {
		t.Fatalf("creating library: %v", err)
	}
	// Lists first: they hold a foreign key on the library, and a failed delete
	// here is logged rather than dropped, which is how fixtures piled up.
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM lists WHERE shared_library_id = $1`,
			`DELETE FROM libraries WHERE id = $1`,
		} {
			if _, err := pool.Exec(ctx, q, libraryID); err != nil {
				t.Logf("cleanup: %v", err)
			}
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})
	return userID, libraryID
}

func seedBook(ctx context.Context, t *testing.T, pool *pgxpool.Pool, title string) uuid.UUID {
	t.Helper()

	var mediaTypeID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types ORDER BY name LIMIT 1`).Scan(&mediaTypeID); err != nil {
		t.Fatalf("no media types seeded: %v", err)
	}
	bookID := uuid.New()
	full := title + " " + bookID.String()
	if _, err := pool.Exec(ctx, `
		INSERT INTO books (id, title, media_type_id, sort_title, title_key)
		VALUES ($1, $2, $3, $4, $5)`, bookID, full, mediaTypeID, full, full); err != nil {
		t.Fatalf("creating book: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, bookID) })
	return bookID
}
