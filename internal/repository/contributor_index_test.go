// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import "testing"

func TestIndexLetter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain surname", "Atwood, Margaret", "A"},
		{"lowercase sort name", "le Guin, Ursula K.", "L"},
		{"leading space", "  Miura, Kentaro", "M"},
		{"digit files under hash", "1Q84", "#"},
		{"empty", "", "#"},
		{"whitespace only", "   ", "#"},
		// An index that files Émile under '#' has lost the name it exists to
		// find, so accents fold to their base letter rather than falling out.
		{"accent folds to its base letter", "Émile Zola", "E"},
		{"nordic letter folds", "Ólafsdóttir, Auður", "O"},
		{"punctuation first", "'t Hooft", "#"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := indexLetter(c.in); got != c.want {
				t.Errorf("indexLetter(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
