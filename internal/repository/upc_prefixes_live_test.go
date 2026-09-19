// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Skipped unless LIBRARIUM_TEST_DSN is set. Uses a UPC company no publisher
// has, and removes it.
func TestUPCPrefixRepo(t *testing.T) {
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
	const company = "999999"
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM upc_isbn_prefixes WHERE upc_prefix = $1`, company); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	repo := NewUPCPrefixRepo(pool)
	if got, err := repo.ISBNPrefixes(ctx, company); err != nil || got != nil {
		t.Fatalf("before learning: %v, %v", got, err)
	}
	added, err := repo.Learn(ctx, company, "0441", "9780441172719", nil)
	if err != nil || !added {
		t.Fatalf("first learn: added %v, %v", added, err)
	}
	// Learning the same pairing again is not an error, and not a second row.
	if added, err := repo.Learn(ctx, company, "0441", "9780441172719", nil); err != nil || added {
		t.Fatalf("second learn: added %v, %v", added, err)
	}
	if _, err := repo.Learn(ctx, company, "0812", "9780812550702", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ISBNPrefixes(ctx, company); !reflect.DeepEqual(got, []string{"0441", "0812"}) {
		t.Errorf("prefixes = %v", got)
	}
}
