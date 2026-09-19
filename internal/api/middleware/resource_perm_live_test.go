// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Before RequireOn, PATCH /locations/{id} and its kind checked only for a
// login: anyone signed in could rename or delete another library's places.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Makes its own users, library and
// place, and removes them.
func TestRequireOn(t *testing.T) {
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

	mk := func(name string) uuid.UUID {
		id := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, username, display_name, email) VALUES ($1, $2, $3, $4)`,
			id, "authz-"+id.String(), "authz fixture "+name, id.String()+"@example.test"); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
		return id
	}
	owner, editor, viewer, outsider := mk("owner"), mk("editor"), mk("viewer"), mk("outsider")
	lib := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO libraries (id, name, slug, owner_id) VALUES ($1, $2, $3, $4)`,
		lib, "authz fixture "+lib.String(), "authz-"+lib.String(), owner); err != nil {
		t.Fatalf("creating library: %v", err)
	}
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM copy_locations WHERE library_id = $1`,
			`DELETE FROM user_roles WHERE library_id = $1`,
			`DELETE FROM libraries WHERE id = $1`,
		} {
			if _, err := pool.Exec(ctx, q, lib); err != nil {
				t.Logf("cleanup: %v", err)
			}
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{owner, editor, viewer, outsider}); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})
	grant := func(user uuid.UUID, role string) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id, scope, library_id)
			SELECT $1, id, 'library', $2 FROM roles WHERE code = $3`, user, lib, role); err != nil {
			t.Fatalf("granting %s: %v", role, err)
		}
	}
	grant(owner, "library_owner")
	grant(editor, "library_editor")
	grant(viewer, "library_viewer")

	place := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO copy_locations (id, library_id, name) VALUES ($1, $2, 'authz fixture place')`, place, lib); err != nil {
		t.Fatalf("creating place: %v", err)
	}

	// A book held through a copy only, as every book added since the tiers
	// migration is: no library_books row.
	var mediaType uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types ORDER BY name LIMIT 1`).Scan(&mediaType); err != nil {
		t.Skipf("no media types: %v", err)
	}
	book, edition := uuid.New(), uuid.New()
	title := "authz fixture book " + book.String()
	if _, err := pool.Exec(ctx, `INSERT INTO books (id, title, media_type_id, sort_title, title_key) VALUES ($1, $2, $3, $4, $5)`,
		book, title, mediaType, title, title); err != nil {
		t.Fatalf("creating book: %v", err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM copies WHERE book_id = $1`, `DELETE FROM books WHERE id = $1`} {
			if _, err := pool.Exec(ctx, q, book); err != nil {
				t.Logf("cleanup: %v", err)
			}
		}
	})
	if _, err := pool.Exec(ctx, `INSERT INTO book_editions (id, book_id, format, is_primary) VALUES ($1, $2, 'paperback', true)`, edition, book); err != nil {
		t.Fatalf("creating edition: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO copies (library_id, book_id, edition_id) VALUES ($1, $2, $3)`, lib, book, edition); err != nil {
		t.Fatalf("creating copy: %v", err)
	}

	call := func(claims *UserClaims, perm string, scope Scope, param string) int {
		h := RequireOn(pool, perm, scope)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
		req := httptest.NewRequest("PATCH", "/x", nil)
		if scope.Param != "" {
			req.SetPathValue(scope.Param, param)
		}
		req = req.WithContext(context.WithValue(req.Context(), claimsKey, claims))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	user := func(id uuid.UUID) *UserClaims { return &UserClaims{UserID: id} }

	cases := []struct {
		name   string
		claims *UserClaims
		perm   string
		scope  Scope
		want   int
	}{
		{"owner edits the place", user(owner), "books:update", ScopeLocation, 200},
		{"editor edits the place", user(editor), "books:update", ScopeLocation, 200},
		{"viewer can't", user(viewer), "books:update", ScopeLocation, 403},
		{"someone from another library can't", user(outsider), "books:update", ScopeLocation, 403},
		{"an admin can", &UserClaims{UserID: outsider, IsInstanceAdmin: true}, "books:update", ScopeLocation, 200},
		{"an owner's token scoped to reading can't", &UserClaims{UserID: owner, FromToken: true, TokenScopes: []string{"books:read"}}, "books:update", ScopeLocation, 403},
		{"editor edits a contributor", user(editor), "contributors:update", ScopeAnyLibrary, 200},
		{"viewer can't edit a contributor", user(viewer), "contributors:update", ScopeAnyLibrary, 403},
		{"editor can't delete a contributor", user(editor), "contributors:delete", ScopeAnyLibrary, 403},
		{"owner can", user(owner), "contributors:delete", ScopeAnyLibrary, 200},
	}
	for _, c := range cases {
		if got := call(c.claims, c.perm, c.scope, place.String()); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	for _, c := range []struct {
		name  string
		who   uuid.UUID
		scope Scope
		param uuid.UUID
		want  int
	}{
		{"editor edits a book held by copy", editor, ScopeBook, book, 200},
		{"editor edits its edition", editor, ScopeEdition, edition, 200},
		{"viewer can't", viewer, ScopeBook, book, 403},
		{"outsider can't", outsider, ScopeEdition, edition, 403},
	} {
		if got := call(user(c.who), "books:update", c.scope, c.param.String()); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	if got := call(user(owner), "books:update", ScopeLocation, "not-a-uuid"); got != 400 {
		t.Errorf("bad id: %d, want 400", got)
	}
	if got := call(user(owner), "books:update", ScopeLocation, uuid.NewString()); got != 403 {
		t.Errorf("a place that doesn't exist: %d, want 403", got)
	}
}
