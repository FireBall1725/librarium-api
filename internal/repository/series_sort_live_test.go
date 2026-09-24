// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The series list files a name without its leading article, the same way book
// titles are, and leaves names that only start like one alone.
//
// Skipped unless LIBRARIUM_TEST_DSN is set. Creates its own series and removes them.
func TestSeriesListIgnoresLeadingArticles(t *testing.T) {
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

	var libraryID, userID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id, owner_id FROM libraries ORDER BY created_at LIMIT 1`).Scan(&libraryID, &userID); err != nil {
		t.Skipf("no libraries: %v", err)
	}

	// The marker goes at the end so it can't change where a name files.
	marker := " zzsort" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	names := []string{"The Lost Fleet", "A Song of Ice and Fire", "Lost Boys", "Asimov's Mysteries", "An Ember in the Ashes", "Dune"}
	repo := NewSeriesRepo(pool)
	for _, n := range names {
		s, err := repo.Create(ctx, uuid.New(), libraryID, n+marker, "", nil, "", "", nil, "", nil, "", "", "", userID)
		if err != nil {
			t.Fatal(err)
		}
		id := s.ID
		t.Cleanup(func() {
			if _, err := pool.Exec(ctx, `DELETE FROM series WHERE id = $1`, id); err != nil {
				t.Logf("cleanup: %v", err)
			}
		})
	}

	list := func(desc bool) []string {
		got, err := repo.ListAcrossFiltered(ctx, []uuid.UUID{libraryID}, userID, strings.TrimSpace(marker), 0, SeriesFilter{Desc: desc})
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(got))
		for _, s := range got {
			out = append(out, strings.TrimSuffix(s.Name, marker))
		}
		return out
	}

	want := []string{"Asimov's Mysteries", "Dune", "An Ember in the Ashes", "Lost Boys", "The Lost Fleet", "A Song of Ice and Fire"}
	if got := list(false); !reflect.DeepEqual(got, want) {
		t.Fatalf("A to Z:\n got %q\nwant %q", got, want)
	}
	for i, j := 0, len(want)-1; i < j; i, j = i+1, j-1 {
		want[i], want[j] = want[j], want[i]
	}
	if got := list(true); !reflect.DeepEqual(got, want) {
		t.Fatalf("Z to A:\n got %q\nwant %q", got, want)
	}
}
