// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// The library filter is the one query parameter that decides what rows a caller
// is allowed to see, so these tests are about the boundary rather than the
// convenience. Every case below is a way the parameter could be made to widen
// the scope instead of narrowing it.

func TestNarrowToReadable(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	readable := []uuid.UUID{a, b}

	t.Run("no request leaves the scope alone", func(t *testing.T) {
		got := narrowToReadable(nil, readable)
		if len(got) != 2 {
			t.Fatalf("want the readable set, got %v", got)
		}
	})

	t.Run("narrows to the intersection", func(t *testing.T) {
		got := narrowToReadable([]uuid.UUID{a}, readable)
		if len(got) != 1 || got[0] != a {
			t.Fatalf("want [a], got %v", got)
		}
	})

	t.Run("cannot widen to a library the caller cannot read", func(t *testing.T) {
		// The whole point. c is not readable, so asking for it must not
		// return it, and must not fall back to everything either.
		got := narrowToReadable([]uuid.UUID{c}, readable)
		if len(got) != 0 {
			t.Fatalf("want nothing, got %v", got)
		}
	})

	t.Run("drops the unreadable half of a mixed request", func(t *testing.T) {
		got := narrowToReadable([]uuid.UUID{a, c}, readable)
		if len(got) != 1 || got[0] != a {
			t.Fatalf("want [a], got %v", got)
		}
	})

	t.Run("a caller who can read nothing reads nothing", func(t *testing.T) {
		got := narrowToReadable([]uuid.UUID{a}, nil)
		if len(got) != 0 {
			t.Fatalf("want nothing, got %v", got)
		}
	})
}

func TestLibraryIDsFromQuery(t *testing.T) {
	id := uuid.New()

	t.Run("absent means no filter", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/x", nil)
		if got := libraryIDsFromQuery(r); got != nil {
			t.Fatalf("want nil so the scope is untouched, got %v", got)
		}
	})

	t.Run("parses a comma separated list", func(t *testing.T) {
		other := uuid.New()
		r := httptest.NewRequest("GET", "/x?lib="+id.String()+","+other.String(), nil)
		if got := libraryIDsFromQuery(r); len(got) != 2 {
			t.Fatalf("want 2 ids, got %v", got)
		}
	})

	t.Run("tolerates surrounding space", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/x?lib=%20"+id.String()+"%20", nil)
		got := libraryIDsFromQuery(r)
		if len(got) != 1 || got[0] != id {
			t.Fatalf("want the id, got %v", got)
		}
	})

	t.Run("junk is a filter matching nothing, not an absent filter", func(t *testing.T) {
		// The dangerous failure: returning nil here would make `?lib=nonsense`
		// mean "every library the caller can read" rather than "none".
		r := httptest.NewRequest("GET", "/x?lib=not-a-uuid", nil)
		got := libraryIDsFromQuery(r)
		if len(got) == 0 {
			t.Fatal("want a filter that matches nothing, got an absent filter")
		}
		if len(narrowToReadable(got, []uuid.UUID{id})) != 0 {
			t.Fatal("junk must not resolve to any readable library")
		}
	})

	t.Run("keeps the good half of a partly junk list", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/x?lib=nope,"+id.String(), nil)
		got := libraryIDsFromQuery(r)
		if len(got) != 1 || got[0] != id {
			t.Fatalf("want just the parseable id, got %v", got)
		}
	})
}
