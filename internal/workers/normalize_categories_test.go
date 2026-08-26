// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package workers

import (
	"testing"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
)

// The genre a provider names is usually inside the phrase rather than equal to
// it, and matching on equality threw away almost everything they send.
//
// Every category string below is one Open Library actually returned for a book
// in a real collection, which is how a catalogue of 1,800 books ended up
// carrying three genres with all three really being the media type.
func TestNormalizeCategoriesFindsTheGenreInsideThePhrase(t *testing.T) {
	names := []string{
		"Action", "Adventure", "Children's", "Comedy", "Crime", "Drama",
		"Fantasy", "Historical", "Horror", "Manga", "Mystery", "Romance",
		"Science Fiction", "Thriller", "Young Adult",
	}
	all := make([]*models.Genre, 0, len(names))
	byName := map[string]uuid.UUID{}
	for _, n := range names {
		g := &models.Genre{ID: uuid.New(), Name: n}
		all = append(all, g)
		byName[n] = g.ID
	}

	cases := []struct {
		name  string
		cats  []string
		want  []string
		avoid []string
	}{{
		name: "natural phrases carry the genre inside them",
		cats: []string{"Horror tales", "Horror stories", "Young adult fiction"},
		want: []string{"Horror", "Young Adult"},
	}, {
		name: "a qualifier in front does not hide it",
		cats: []string{"legal thriller"},
		want: []string{"Thriller"},
	}, {
		name: "a BISAC path still splits and still matches",
		cats: []string{"Comics & Graphic Novels / Manga / General"},
		want: []string{"Manga"},
	}, {
		name: "two genres in one phrase both count",
		cats: []string{"Science Fiction & Fantasy"},
		want: []string{"Science Fiction", "Fantasy"},
	}, {
		// The reason boundaries exist. A wrong genre on somebody's book is
		// worse than a missing one: it is a claim rather than a gap.
		name:  "a genre buried inside a longer word is not a match",
		cats:  []string{"Melodrama", "Romanesque architecture"},
		avoid: []string{"Drama", "Romance"},
	}, {
		name:  "a shelf category that is not a genre matches nothing",
		cats:  []string{"Fiction", "Literature", "General"},
		avoid: names,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := map[uuid.UUID]bool{}
			for _, id := range normalizeCategories(tc.cats, all) {
				got[id] = true
			}
			for _, w := range tc.want {
				if !got[byName[w]] {
					t.Errorf("%v did not yield %q", tc.cats, w)
				}
			}
			for _, a := range tc.avoid {
				if got[byName[a]] {
					t.Errorf("%v wrongly yielded %q", tc.cats, a)
				}
			}
		})
	}
}
