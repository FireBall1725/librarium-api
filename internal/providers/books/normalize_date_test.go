// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package books

import "testing"

// Google Books and Open Library send "1987" when that's all they know; it
// used to come out as 1987-01-01, a date nobody could tell from a real one.
func TestNormalizeDate_KeepsPrecision(t *testing.T) {
	for in, want := range map[string]string{
		"1987":           "1987",
		"1987-08":        "1987-08",
		"1987-08-15":     "1987-08-15",
		"1987-01-01":     "1987-01-01", // a real 1 January stays
		"August 1987":    "1987-08",
		"Aug 15, 1987":   "1987-08-15",
		"15 August 1987": "1987-08-15",
		"sometime":       "",
		"":               "",
	} {
		if got := normalizeDate(in); got != want {
			t.Errorf("normalizeDate(%q) = %q, want %q", in, got, want)
		}
	}
}
