// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A place becomes a bookcase by setting shelf_count, and each field changes
// only when the request touched it.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Creates its own places and removes them.
func TestSetBookcase(t *testing.T) {
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

	var libraryID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries ORDER BY created_at LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no libraries: %v", err)
	}
	repo := NewCopyLocationRepo(pool)
	room, err := repo.Create(ctx, libraryID, "bookcase fixture room "+uuid.NewString(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM copy_locations WHERE id = $1 OR parent_id = $1`, room.ID) })
	bc, err := repo.Create(ctx, libraryID, "Bookcase A", &room.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bc.ShelfCount != nil || bc.ShelfNumbering != nil {
		t.Fatalf("a new place is not a bookcase: %+v", bc)
	}

	six, up := 6, "bottom_up"
	if err := repo.SetBookcase(ctx, bc.ID, BookcaseChange{SetCount: true, Count: &six, SetNumbering: true, Numbering: &up}); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.FindByID(ctx, bc.ID)
	if got.ShelfCount == nil || *got.ShelfCount != 6 || got.ShelfNumbering == nil || *got.ShelfNumbering != "bottom_up" {
		t.Fatalf("after setting: %+v", got)
	}

	// Touching only the count leaves the numbering alone.
	eight := 8
	if err := repo.SetBookcase(ctx, bc.ID, BookcaseChange{SetCount: true, Count: &eight}); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.FindByID(ctx, bc.ID)
	if *got.ShelfCount != 8 || got.ShelfNumbering == nil || *got.ShelfNumbering != "bottom_up" {
		t.Fatalf("after changing the count: %+v", got)
	}

	// The list carries the fields too.
	all, err := repo.List(ctx, libraryID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range all {
		if l.ID == bc.ID && l.ShelfCount != nil && *l.ShelfCount == 8 {
			found = true
		}
	}
	if !found {
		t.Error("List didn't return the shelf count")
	}

	// Clearing the count turns it back into a plain place.
	if err := repo.SetBookcase(ctx, bc.ID, BookcaseChange{SetCount: true}); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.FindByID(ctx, bc.ID)
	if got.ShelfCount != nil {
		t.Fatalf("after clearing: %+v", got)
	}

	zero := 0
	if err := repo.SetBookcase(ctx, bc.ID, BookcaseChange{SetCount: true, Count: &zero}); !errors.Is(err, ErrBadBookcase) {
		t.Errorf("zero shelves: want ErrBadBookcase, got %v", err)
	}
	if err := repo.SetBookcase(ctx, uuid.New(), BookcaseChange{SetCount: true, Count: &six}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown place: want ErrNotFound, got %v", err)
	}
}
