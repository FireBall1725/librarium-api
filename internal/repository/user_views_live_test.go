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

// TestUserViewsRoundTrip covers the behaviour the rail depends on: a user who
// has never asked gets the built-ins, edits stick, the Default refuses to be
// deleted, and deleting an ordinary view does not bring the built-ins back.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Cleans up after itself.
func TestUserViewsRoundTrip(t *testing.T) {
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

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users ORDER BY created_at LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no users: %v", err)
	}
	repo := NewUserViewRepo(pool)
	// Start clean and leave clean: this user's real views are not the fixture.
	wipe := func() { _, _ = pool.Exec(ctx, `DELETE FROM user_views WHERE user_id = $1`, userID) }
	wipe()
	defer wipe()

	// First read seeds.
	got, err := repo.List(ctx, userID)
	if err != nil {
		t.Fatalf("first list: %v", err)
	}
	if len(got) != len(BuiltInViews) {
		t.Fatalf("first list returned %d views, want the %d built-ins", len(got), len(BuiltInViews))
	}
	if got[0].ID != "default" || !got[0].Permanent || !got[0].Hidden {
		t.Errorf("first view = %+v, want the hidden permanent Default", got[0])
	}

	// Second read does not seed again.
	again, err := repo.List(ctx, userID)
	if err != nil || len(again) != len(got) {
		t.Fatalf("second list returned %d, want %d (err %v)", len(again), len(got), err)
	}

	// Updating the Default is how Books is pointed somewhere else.
	if err := repo.Upsert(ctx, userID, UserView{
		ID: "default", Name: "Default", Params: "status=unread", Layout: "grid",
		Permanent: true, Hidden: true, BuiltIn: true,
	}); err != nil {
		t.Fatalf("updating default: %v", err)
	}
	after, _ := repo.List(ctx, userID)
	var def UserView
	for _, v := range after {
		if v.ID == "default" {
			def = v
		}
	}
	if def.Params != "status=unread" {
		t.Errorf("default params = %q, want the update to stick", def.Params)
	}
	if !def.Permanent {
		t.Errorf("default lost its permanence on update")
	}

	// The Default cannot go.
	if err := repo.Delete(ctx, userID, "default"); err != ErrViewPermanent {
		t.Errorf("deleting the default returned %v, want ErrViewPermanent", err)
	}

	// An ordinary one can, and stays gone.
	if err := repo.Delete(ctx, userID, "signed"); err != nil {
		t.Fatalf("deleting an ordinary view: %v", err)
	}
	final, _ := repo.List(ctx, userID)
	for _, v := range final {
		if v.ID == "signed" {
			t.Errorf("a deleted view came back on the next read")
		}
	}
	if len(final) != len(BuiltInViews)-1 {
		t.Errorf("got %d views after one delete, want %d", len(final), len(BuiltInViews)-1)
	}

	// Deleting something that is not there is a miss, not a crash.
	if err := repo.Delete(ctx, userID, "nope"); err != ErrNotFound {
		t.Errorf("deleting a missing view returned %v, want ErrNotFound", err)
	}
}
