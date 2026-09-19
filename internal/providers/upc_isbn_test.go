// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package providers

import (
	"reflect"
	"testing"
)

// Checked against the book: Hominids has UPC 037145007991, add-on 34500, and
// ISBN 9780765345004 inside the cover.
func TestISBNsFromUPCAddon(t *testing.T) {
	if got := ISBNsFromUPCAddon("03714500799134500"); !reflect.DeepEqual(got, []string{"9780765345004"}) {
		t.Errorf("Hominids: got %v", got)
	}
	for _, code := range []string{
		"037145007991",      // no add-on: nothing to build from
		"03600029145200399", // a publisher not in the table
		"0371450079913450",  // too short
		"037145007991345OO", // not digits
	} {
		if got := ISBNsFromUPCAddon(code); got != nil {
			t.Errorf("%s: want nothing, got %v", code, got)
		}
	}
}

func TestISBN13From10Body(t *testing.T) {
	// Dune: 0-441-17271-7 is 978-0-441-17271-9.
	if got := isbn13From10Body("044117271"); got != "9780441172719" {
		t.Errorf("got %s", got)
	}
}
