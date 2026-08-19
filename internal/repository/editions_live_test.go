// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestInteractionFavouriteRoundTrip checks that starring an edition persists
// and reads back, which is what "the checkbox does not save" would mean at this
// layer.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Writes and then removes its own row.
func TestInteractionFavouriteRoundTrip(t *testing.T) {
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

	var userID, editionID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users ORDER BY created_at LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no users: %v", err)
	}
	// An edition nobody has an interaction on, so the test starts from the
	// insert branch rather than an existing row.
	if err := pool.QueryRow(ctx, `
        SELECT e.id FROM book_editions e
        WHERE NOT EXISTS (SELECT 1 FROM user_book_interactions i WHERE i.book_edition_id = e.id)
        LIMIT 1`).Scan(&editionID); err != nil {
		t.Skipf("no untouched edition: %v", err)
	}

	repo := &EditionRepo{db: pool}
	defer func() { _ = repo.DeleteInteraction(ctx, userID, editionID) }()

	// Typed nils, exactly as the service passes them: an untyped nil changes
	// how Postgres infers the parameter's type and would test a call the
	// application never makes.
	var rating *int
	var started, finished *time.Time

	// Insert branch.
	got, err := repo.UpsertInteraction(ctx, userID, editionID, "unread", rating, "", "", started, finished, true, nil)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if !got.IsFavorite {
		t.Errorf("insert returned IsFavorite=false, want true")
	}

	// Read back through the path the form GET uses.
	read, err := repo.GetInteraction(ctx, userID, editionID)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !read.IsFavorite {
		t.Errorf("read back IsFavorite=false, want true — this is the bug")
	}

	// Conflict branch: starring again on an existing row, then unstarring.
	if _, err := repo.UpsertInteraction(ctx, userID, editionID, "reading", rating, "", "", started, finished, false, nil); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	read, err = repo.GetInteraction(ctx, userID, editionID)
	if err != nil {
		t.Fatalf("find after update: %v", err)
	}
	if read.IsFavorite {
		t.Errorf("unstarring did not stick")
	}
}
