// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import "testing"

func TestIsUPCOrEAN(t *testing.T) {
	for code, want := range map[string]bool{
		"036000291452":       true, // UPC-A
		"4006381333931":      true, // EAN-13
		"03600029145200399":  true, // UPC-A with an add-on
		"75960608790700111":  true, // comic: the add-on picks the issue
		"978044117271951099": true, // EAN-13 with a price add-on
		"0360002914520039":   false,
		"03600029145":        false,
		"03600029145X":       false,
		"":                   false,
	} {
		if got := isUPCOrEAN(code); got != want {
			t.Errorf("isUPCOrEAN(%q) = %v, want %v", code, got, want)
		}
	}
}
