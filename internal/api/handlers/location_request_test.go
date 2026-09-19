// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"strings"
	"testing"

	"github.com/fireball1725/librarium-api/internal/repository"
)

func bookcase(count *int, numbering *string) repository.BookcaseChange {
	return repository.BookcaseChange{SetCount: count != nil, Count: count, SetNumbering: numbering != nil, Numbering: numbering}
}

func TestParseLocationRequest(t *testing.T) {
	parse := func(body string) locationRequest {
		t.Helper()
		r, err := parseLocationRequest(strings.NewReader(body))
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		return r
	}

	// Absent leaves everything alone.
	r := parse(`{"name":"Bookcase A"}`)
	if r.Name != "Bookcase A" || r.SetParent || r.Bookcase.SetCount || r.Bookcase.SetNumbering {
		t.Errorf("name only: %+v", r)
	}

	// null moves to the top level. It used to decode the same as absent, so the
	// move silently did nothing.
	r = parse(`{"parent_id":null}`)
	if !r.SetParent || r.Parent != nil {
		t.Errorf("parent_id null: %+v", r)
	}
	r = parse(`{"parent_id":""}`)
	if !r.SetParent || r.Parent != nil {
		t.Errorf("parent_id empty: %+v", r)
	}
	r = parse(`{"parent_id":"6e425ec0-9a01-480c-ac66-2baba0e19dc2"}`)
	if !r.SetParent || r.Parent == nil || r.Parent.String() != "6e425ec0-9a01-480c-ac66-2baba0e19dc2" {
		t.Errorf("parent_id set: %+v", r)
	}

	// A value sets, null clears.
	r = parse(`{"shelf_count":6,"shelf_numbering":"bottom_up"}`)
	if !r.Bookcase.SetCount || r.Bookcase.Count == nil || *r.Bookcase.Count != 6 ||
		!r.Bookcase.SetNumbering || r.Bookcase.Numbering == nil || *r.Bookcase.Numbering != "bottom_up" {
		t.Errorf("bookcase set: %+v", r.Bookcase)
	}
	r = parse(`{"shelf_count":null}`)
	if !r.Bookcase.SetCount || r.Bookcase.Count != nil || r.Bookcase.SetNumbering {
		t.Errorf("shelf_count null: %+v", r.Bookcase)
	}

	for _, bad := range []string{`not json`, `{"parent_id":"nope"}`, `{"shelf_count":"six"}`, `{"shelf_numbering":3}`} {
		if _, err := parseLocationRequest(strings.NewReader(bad)); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}

func TestValidBookcase(t *testing.T) {
	n := func(v int) *int { return &v }
	s := func(v string) *string { return &v }
	ok := []locationRequest{
		{},
		{Bookcase: bookcase(n(1), nil)},
		{Bookcase: bookcase(n(50), s("top_down"))},
		{Bookcase: bookcase(nil, s("bottom_up"))},
	}
	for _, r := range ok {
		if err := validBookcase(r.Bookcase); err != nil {
			t.Errorf("%+v: %v", r.Bookcase, err)
		}
	}
	for _, r := range []locationRequest{
		{Bookcase: bookcase(n(0), nil)},
		{Bookcase: bookcase(n(51), nil)},
		{Bookcase: bookcase(nil, s("sideways"))},
	} {
		if err := validBookcase(r.Bookcase); err == nil {
			t.Errorf("%+v: want an error", r.Bookcase)
		}
	}
}
