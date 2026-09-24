// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Editors can delete a series (migration 46) but contributor and loan deletes
// stay with owners.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Reads only.
func TestEditorPermissionsOnDeletes(t *testing.T) {
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

	for perm, want := range map[string]bool{
		"series:delete":       true,
		"contributors:delete": false,
		"loans:delete":        false,
	} {
		var got bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM role_permissions rp
			                 JOIN roles r ON r.id = rp.role_id
			                 JOIN permissions p ON p.id = rp.permission_id
			                WHERE r.code = 'library_editor' AND p.name = $1)`, perm).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("library_editor %s: got %v, want %v", perm, got, want)
		}
	}
}
