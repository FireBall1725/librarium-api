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
	// The same scan as a camera reports it: the UPC as a 13-digit EAN.
	if got := ISBNsFromUPCAddon("003714500799134500"); !reflect.DeepEqual(got, []string{"9780765345004"}) {
		t.Errorf("Hominids from a camera: got %v", got)
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

func TestISBNsFromUPCAddonUsesLearnedPrefixes(t *testing.T) {
	// A publisher the code doesn't know, learned by the instance.
	if got := ISBNsFromUPCAddon("07522500799134500", "0441"); !reflect.DeepEqual(got, []string{isbn13From10Body("044134500")}) {
		t.Errorf("learned prefix: got %v", got)
	}
	// Tor's checked prefix comes first, and a repeat of it isn't tried twice.
	got := ISBNsFromUPCAddon("03714500799134500", "0765", "0812")
	want := []string{"9780765345004", isbn13From10Body("081234500")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("checked then learned: got %v, want %v", got, want)
	}
}

func TestLearnableUPCPrefix(t *testing.T) {
	// Hybrids: back cover 037145007991 + 34906, ISBN 9780765349064.
	company, prefix, ok := LearnableUPCPrefix("03714500799134906", "9780765349064")
	if !ok || company != "037145" || prefix != "0765" {
		t.Errorf("Hybrids: %q %q %v", company, prefix, ok)
	}
	if _, _, ok := LearnableUPCPrefix("003714500799134906", "9780765349064"); !ok {
		t.Error("the camera's 18-digit form should work too")
	}
	for _, c := range []struct{ code, isbn string }{
		{"03714500799134500", "9780765349064"}, // add-on is a different book
		{"037145007991", "9780765345004"},      // no add-on
		{"03714500799134500", "9790765345000"}, // 979 has no ISBN-10
		{"03714500799134500", "9780765345005"}, // bad check digit
	} {
		if _, _, ok := LearnableUPCPrefix(c.code, c.isbn); ok {
			t.Errorf("%s + %s should not pair", c.code, c.isbn)
		}
	}
}
