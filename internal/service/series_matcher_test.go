// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import "testing"

func TestMatchTitleToSeries(t *testing.T) {
	cases := []struct {
		name    string
		title   string
		series  string
		wantPos float64
		wantOK  bool
	}{
		{"hash marker", "Bleach #1: The Death and the Strawberry", "Bleach", 1, true},
		{"vol marker", "Bleach, Vol. 3", "Bleach", 3, true},
		{"volume word", "Bleach Volume 7", "Bleach", 7, true},
		{"bare number", "Bleach 5", "Bleach", 5, true},
		{"colon number", "Bleach: 2", "Bleach", 2, true},
		{"fractional", "Bleach #1.5", "Bleach", 1.5, true},
		{"case insensitive", "BLEACH #4", "Bleach", 4, true},
		{"case insensitive series", "bleach vol 9", "BLEACH", 9, true},
		{"multi-word series", "One Piece, Vol. 12", "One Piece", 12, true},
		{"no volume", "Bleach: Fade to Black", "Bleach", 0, false},
		{"false prefix", "Bleachers", "Bleach", 0, false},
		{"false prefix number", "Bleach7Wonders 1", "Bleach", 0, false},
		{"empty title", "", "Bleach", 0, false},
		{"empty series", "Bleach 1", "", 0, false},
		{"series only", "Bleach", "Bleach", 0, false},
		{"number before series", "The Bleach 1", "Bleach", 0, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pos, ok := MatchTitleToSeries(c.title, c.series)
			if ok != c.wantOK {
				t.Fatalf("MatchTitleToSeries(%q, %q) ok = %v, want %v", c.title, c.series, ok, c.wantOK)
			}
			if ok && pos != c.wantPos {
				t.Fatalf("MatchTitleToSeries(%q, %q) pos = %v, want %v", c.title, c.series, pos, c.wantPos)
			}
		})
	}
}
