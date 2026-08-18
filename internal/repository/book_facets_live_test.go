// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestFacetsAgainstLiveDB runs the real facet query, which is the only way to
// catch a mismatch between the format string's placeholders and its arguments:
// that produces broken SQL at runtime, not a compile error.
//
// Skipped unless LIBRARIUM_TEST_DSN is set, so the default `go test` path still
// needs no database.
func TestFacetsAgainstLiveDB(t *testing.T) {
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

	var libs []uuid.UUID
	rows, err := pool.Query(ctx, `SELECT id FROM libraries`)
	if err != nil {
		t.Fatalf("listing libraries: %v", err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		libs = append(libs, id)
	}
	rows.Close()

	if len(libs) == 0 {
		t.Skip("no libraries in the target database")
	}

	// Any user will do: the point is that the query runs and the dimension is
	// whole, not that a particular collection holds particular books.
	var caller uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users ORDER BY created_at LIMIT 1`).Scan(&caller); err != nil {
		t.Skipf("no users in the target database: %v", err)
	}

	repo := &BookRepo{db: pool}

	// Unfiltered, then with the new dimension actually selected, since the
	// selected path builds a different expression and binds a parameter.
	for _, sel := range []FacetSelection{{}, {Favourites: []bool{true}}} {
		got, err := repo.Facets(ctx, libs, sel, "", nil, caller)
		if err != nil {
			t.Fatalf("Facets(%v): %v", sel.Favourites, err)
		}
		if len(got.Favourite) != len(FavouriteValues) {
			t.Errorf("favourite facet = %v, want both values zero-filled", got.Favourite)
		}
		for _, v := range got.Favourite {
			t.Logf("sel=%v favourite %s = %d", sel.Favourites, v.Value, v.Count)
		}
	}
}
