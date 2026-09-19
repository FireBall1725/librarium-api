// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package workers

import (
	"testing"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/providers"
)

// A forced refresh used to write the provider's value even when it was empty,
// so a lookup that came back with nothing blanked the title and the ISBN.
func TestFillField(t *testing.T) {
	cases := []struct {
		name                  string
		current, merged, want string
		force                 bool
	}{
		{"fills a gap", "", "Dune", "Dune", false},
		{"keeps what's there", "Dune", "DUNE", "Dune", false},
		{"force prefers the provider", "Dune", "Dune Messiah", "Dune Messiah", true},
		{"force never clears", "Dune", "", "Dune", true},
		{"nothing either side", "", "", "", true},
	}
	for _, c := range cases {
		if got := fillField(c.current, c.merged, c.force); got != c.want {
			t.Errorf("%s: fillField(%q, %q, %v) = %q, want %q", c.name, c.current, c.merged, c.force, got, c.want)
		}
	}
}

func TestMergedIsEmpty(t *testing.T) {
	if !mergedIsEmpty(nil) || !mergedIsEmpty(&providers.MergedBookResult{}) {
		t.Error("no providers answering must count as empty")
	}
	if !mergedIsEmpty(&providers.MergedBookResult{Title: &providers.FieldResult{Value: ""}}) {
		t.Error("a field with an empty value is still empty")
	}
	if mergedIsEmpty(&providers.MergedBookResult{Title: &providers.FieldResult{Value: "Dune"}}) {
		t.Error("a title is a result")
	}
	if mergedIsEmpty(&providers.MergedBookResult{Covers: []providers.CoverOption{{}}}) {
		t.Error("a cover alone is a result")
	}
}

func TestPickPublishDate(t *testing.T) {
	stored := time.Date(1965, 1, 1, 0, 0, 0, 0, time.UTC)

	// Not forced and a date exists: untouched, and no precision stated so the
	// repository keeps "year" instead of turning it into 1 January.
	if d, p := pickPublishDate(&stored, "1965-08-01", false); d != &stored || p != "" {
		t.Errorf("unforced with a date: got %v %q", d, p)
	}
	// Forced with nothing from the provider: untouched.
	if d, p := pickPublishDate(&stored, "", true); d != &stored || p != "" {
		t.Errorf("forced, empty provider: got %v %q", d, p)
	}
	// Filling a gap carries the provider's own precision.
	d, p := pickPublishDate(nil, "1965", false)
	if d == nil || d.Year() != 1965 || p != models.DatePrecisionYear {
		t.Errorf("year-only fill: got %v %q", d, p)
	}
	d, p = pickPublishDate(&stored, "August 1965", true)
	if d == nil || d.Month() != time.August || p != models.DatePrecisionMonth {
		t.Errorf("forced month: got %v %q", d, p)
	}
	// Unparseable provider date: keep what's there.
	if d, p := pickPublishDate(nil, "sometime", true); d != nil || p != "" {
		t.Errorf("junk date: got %v %q", d, p)
	}
}
