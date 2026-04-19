// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import "testing"

func TestDeriveSortName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"Cher", "Cher"},
		{"Neil Gaiman", "Gaiman, Neil"},
		{"J.K. Rowling", "Rowling, J.K."},
		{"H. P. Lovecraft", "Lovecraft, H. P."},
		{"Robert Downey Jr.", "Downey, Robert Jr."},
		{"Martin Luther King Jr.", "King, Martin Luther Jr."},
		{"Ludwig van Beethoven", "van Beethoven, Ludwig"},
		{"Johann Sebastian von Bach", "von Bach, Johann Sebastian"},
		{"Leonardo da Vinci", "da Vinci, Leonardo"},
		{"Gaiman, Neil", "Gaiman, Neil"}, // passthrough
		{"ABC Publishing House", "House, ABC Publishing"},
		// The corporate case above shows why is_corporate must be explicit;
		// DeriveSortName doesn't try to detect it.
	}
	for _, tc := range cases {
		got := DeriveSortName(tc.in)
		if got != tc.want {
			t.Errorf("DeriveSortName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
