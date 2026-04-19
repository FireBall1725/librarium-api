// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import "testing"

func TestParseSuggestionsEmDash(t *testing.T) {
	out := ParseSuggestions(`
## Books to buy
1. The Three-Body Problem — 9780765382030 — Hard sci-fi with cosmological scale, matches your rated-high Le Guin.
2. Piranesi — 9781635575637 — Literary fantasy with atmospheric worldbuilding.

## Books to read next
1. Blindsight — You own this unread and it pairs with your sci-fi interests.
`)
	if len(out) != 3 {
		t.Fatalf("want 3 entries, got %d: %+v", len(out), out)
	}
	if out[0].Title != "The Three-Body Problem" {
		t.Errorf("title[0] = %q", out[0].Title)
	}
	if out[0].ISBN != "9780765382030" {
		t.Errorf("isbn[0] = %q", out[0].ISBN)
	}
	if out[2].ISBN != "" {
		t.Errorf("read_next row should not have ISBN, got %q", out[2].ISBN)
	}
	if out[2].Title != "Blindsight" {
		t.Errorf("title[2] = %q", out[2].Title)
	}
}

func TestParseSuggestionsHyphenSeparator(t *testing.T) {
	out := ParseSuggestions(`
1) A Memory Called Empire - 978-1-250-18643-2 - Political sci-fi from a linguist.
2) Ancillary Justice - 9780316246620 - Wide-scope space opera.
`)
	if len(out) != 2 {
		t.Fatalf("want 2 entries, got %d", len(out))
	}
	if out[0].ISBN != "9781250186432" {
		t.Errorf("dashes should be stripped, got %q", out[0].ISBN)
	}
}

func TestFuzzyTitleMatch(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"The Three-Body Problem", "Three Body Problem", true},
		{"A Memory Called Empire", "a memory called empire", true},
		{"Blindsight", "Echopraxia", false},
		{"", "Something", false},
	}
	for _, c := range cases {
		if got := fuzzyTitleMatch(c.a, c.b); got != c.want {
			t.Errorf("fuzzyTitleMatch(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestNormalizeTitle(t *testing.T) {
	if got := normalizeTitle("  The   Three-Body Problem!!! "); got != "the three body problem" {
		t.Errorf("normalize = %q", got)
	}
}
