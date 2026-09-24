// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A record from one library can't be reached through another library's path.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Makes its own user, two libraries,
// a series, a book and editions, and removes them.
func TestRequireInLibrary(t *testing.T) {
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

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", strings.Fields(q)[0:3], err)
		}
	}
	user, libA, libB := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO users (id, username, display_name, email) VALUES ($1, $2, 'scope fixture', $3)`,
		user, "scope-"+user.String(), user.String()+"@example.test")
	for _, l := range []uuid.UUID{libA, libB} {
		exec(`INSERT INTO libraries (id, name, slug, owner_id) VALUES ($1, $2, $3, $4)`,
			l, "scope fixture "+l.String(), "scope-"+l.String(), user)
	}
	var mediaType uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types ORDER BY name LIMIT 1`).Scan(&mediaType); err != nil {
		t.Skipf("no media types: %v", err)
	}
	book, other, edition, otherEdition, series := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM copies WHERE library_id = ANY($1)`,
			`DELETE FROM series WHERE library_id = ANY($1)`,
			`DELETE FROM libraries WHERE id = ANY($1)`,
		} {
			if _, err := pool.Exec(ctx, q, []uuid.UUID{libA, libB}); err != nil {
				t.Logf("cleanup: %v", err)
			}
		}
		if _, err := pool.Exec(ctx, `DELETE FROM books WHERE id = ANY($1)`, []uuid.UUID{book, other}); err != nil {
			t.Logf("cleanup: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, user); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})
	for _, b := range []uuid.UUID{book, other} {
		title := "scope fixture book " + b.String()
		exec(`INSERT INTO books (id, title, media_type_id, sort_title, title_key) VALUES ($1, $2, $3, $4, $5)`, b, title, mediaType, title, title)
	}
	exec(`INSERT INTO book_editions (id, book_id, format, is_primary) VALUES ($1, $2, 'paperback', true)`, edition, book)
	exec(`INSERT INTO book_editions (id, book_id, format, is_primary) VALUES ($1, $2, 'paperback', true)`, otherEdition, other)
	exec(`INSERT INTO copies (library_id, book_id, edition_id) VALUES ($1, $2, $3)`, libA, book, edition)
	exec(`INSERT INTO series (id, library_id, name) VALUES ($1, $2, 'scope fixture series')`, series, libA)

	mux := http.NewServeMux()
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	for _, p := range []string{
		"PUT /api/v1/libraries/{library_id}/series/{series_id}",
		"DELETE /api/v1/libraries/{library_id}/series/{series_id}/books/{book_id}",
		"GET /api/v1/libraries/{library_id}/books/{book_id}",
		"POST /api/v1/libraries/{library_id}/books/{book_id}",
		"PUT /api/v1/libraries/{library_id}/books/{book_id}",
		"PUT /api/v1/libraries/{library_id}/books/{book_id}/editions/{edition_id}",
	} {
		mux.Handle(p, RequireInLibrary(pool)(ok))
	}
	call := func(method, path string) int {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec.Code
	}
	a, b := "/api/v1/libraries/"+libA.String(), "/api/v1/libraries/"+libB.String()
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"PUT", a + "/series/" + series.String(), 200},
		{"PUT", b + "/series/" + series.String(), 404},
		{"PUT", a + "/series/" + uuid.NewString(), 404},
		{"DELETE", b + "/series/" + series.String() + "/books/" + book.String(), 404},
		{"PUT", a + "/books/" + book.String(), 200},
		{"PUT", b + "/books/" + book.String(), 404},
		{"PUT", a + "/books/" + uuid.NewString(), 404},
		{"GET", b + "/books/" + book.String(), 200},
		{"POST", b + "/books/" + book.String(), 200},
		{"PUT", a + "/books/" + book.String() + "/editions/" + edition.String(), 200},
		{"PUT", a + "/books/" + book.String() + "/editions/" + otherEdition.String(), 404},
		{"PUT", a + "/series/not-a-uuid", 400},
	} {
		if got := call(c.method, c.path); got != c.want {
			t.Errorf("%s %s: got %d, want %d", c.method, strings.Replace(strings.Replace(c.path, libA.String(), "A", 1), libB.String(), "B", 1), got, c.want)
		}
	}
}
