// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package providers

import "testing"

func TestBarcodeKey(t *testing.T) {
	for in, want := range map[string]string{
		"0441172717":        "9780441172719",
		"080442957X":        "9780804429573",
		"978-0-441-17271-9": "9780441172719",
		"036000291452":      "036000291452",
		"04411727xx":        "04411727XX",
	} {
		if got := BarcodeKey(in); got != want {
			t.Errorf("BarcodeKey(%q) = %q, want %q", in, got, want)
		}
	}
}
