// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Inventory lists a place's copies by title, the unshelved ones newest first,
// with their books, and counts the library.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Makes its own library, books,
// place, copies and loan, and removes them.
func TestInventory(t *testing.T) {
	dsn := os.Getenv("LIBRARIUM_TEST_DSN")
	if dsn == "" {
		t.Skip("set LIBRARIUM_TEST_DSN to run")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)

	var owner, mediaType uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users ORDER BY created_at LIMIT 1`).Scan(&owner); err != nil {
		t.Skipf("no users: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types LIMIT 1`).Scan(&mediaType); err != nil {
		t.Skipf("no media types: %v", err)
	}

	lib := uuid.New()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO libraries (id, name, slug, owner_id) VALUES ($1, 'inventory fixture', $2, $3)`, lib, "inventory-fixture-"+lib.String(), owner)
	books := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	titles := []string{"Zebra Fixture", "Apple Fixture", "Middle Fixture"}
	// Cleanup runs even when a step below fails, in reverse order of what
	// depends on what.
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM loans WHERE library_id = $1`,
			`DELETE FROM copies WHERE library_id = $1`,
			`DELETE FROM copy_locations WHERE library_id = $1`,
			`DELETE FROM libraries WHERE id = $1`,
		} {
			if _, err := pool.Exec(ctx, q, lib); err != nil {
				t.Logf("cleanup %q: %v", q, err)
			}
		}
		for _, b := range books {
			if _, err := pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, b); err != nil {
				t.Logf("cleanup book: %v", err)
			}
		}
	})
	for i, b := range books {
		exec(`INSERT INTO books (id, title, media_type_id) VALUES ($1, $2, $3)`, b, titles[i], mediaType)
	}
	locations := NewCopyLocationRepo(pool)
	room, err := locations.Create(ctx, lib, "A room", nil)
	if err != nil {
		t.Fatal(err)
	}
	place, err := locations.Create(ctx, lib, "Shelf 1", &room.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Zebra and Apple on the shelf, Middle unshelved, and Apple out on loan.
	var zebra, apple, middle uuid.UUID
	must := func(id *uuid.UUID, q string, args ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, q, args...).Scan(id); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	must(&zebra, `INSERT INTO copies (library_id, book_id, location_id) VALUES ($1, $2, $3) RETURNING id`, lib, books[0], place.ID)
	must(&apple, `INSERT INTO copies (library_id, book_id, location_id) VALUES ($1, $2, $3) RETURNING id`, lib, books[1], place.ID)
	must(&middle, `INSERT INTO copies (library_id, book_id) VALUES ($1, $2) RETURNING id`, lib, books[2])
	exec(`INSERT INTO loans (library_id, book_id, copy_id, loaned_to) VALUES ($1, $2, $3, 'a friend')`, lib, books[1], apple)

	repo := NewCopyRepo(pool)
	onShelf, total, err := repo.ListForInventory(ctx, lib, InventoryFilter{LocationID: &place.ID}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(onShelf) != 2 || onShelf[0].BookTitle != "Apple Fixture" || onShelf[1].BookTitle != "Zebra Fixture" {
		t.Fatalf("shelf: total %d, got %+v", total, onShelf)
	}
	if onShelf[0].OnLoanTo != "a friend" {
		t.Errorf("the loan didn't come through: %q", onShelf[0].OnLoanTo)
	}

	// The room holds nothing itself; its shelf's copies only count with Inside.
	_, total, err = repo.ListForInventory(ctx, lib, InventoryFilter{LocationID: &room.ID}, 50, 0)
	if err != nil || total != 0 {
		t.Fatalf("room alone: total %d, err %v", total, err)
	}
	_, total, err = repo.ListForInventory(ctx, lib, InventoryFilter{LocationID: &room.ID, Inside: true}, 50, 0)
	if err != nil || total != 2 {
		t.Fatalf("room and inside: total %d, err %v", total, err)
	}

	unshelved, total, err := repo.ListForInventory(ctx, lib, InventoryFilter{Unshelved: true}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || unshelved[0].ID != middle {
		t.Fatalf("unshelved: total %d, got %+v", total, unshelved)
	}

	all, total, err := repo.ListForInventory(ctx, lib, InventoryFilter{}, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(all) != 2 {
		t.Fatalf("all: total %d, page of %d", total, len(all))
	}

	s, err := repo.SummaryForInventory(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	if s != (InventorySummary{Copies: 3, Shelved: 2, Unshelved: 1, OnLoan: 1}) {
		t.Fatalf("summary: %+v", s)
	}
}
