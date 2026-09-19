// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package providers

// A mass-market paperback's UPC doesn't name the book. Its 12 digits are the
// publisher and the cover price, so every book at that price shares them, and
// a UPC database returns whichever one someone listed. The 5-digit add-on
// printed beside it is the middle of the ISBN-10: publisher prefix, add-on,
// check digit. Tor's 037145007991 with add-on 34500 is 0-7653-4500-5,
// Hominids.
//
// Which ISBN prefix goes with which UPC company can't be worked out, so it's
// a table. Only add an entry checked against a real book: a wrong prefix
// builds a valid ISBN for some other title.
var upcISBNPrefixes = map[string][]string{
	"037145": {"0765"}, // Tor: 037145007991 + 34500 is 0-7653-4500-5, Hominids
}

// ISBNsFromUPCAddon returns the ISBN-13s a UPC-A with a 5-digit add-on could
// stand for, most likely first. Empty when there's no add-on or the
// publisher isn't in the table.
func ISBNsFromUPCAddon(code string) []string {
	if len(code) != 17 || !allDigits(code) {
		return nil
	}
	upc, addon := code[:12], code[12:]
	var out []string
	for _, prefix := range upcISBNPrefixes[upc[:6]] {
		body := prefix + addon
		if len(body) != 9 {
			continue
		}
		out = append(out, isbn13From10Body(body))
	}
	return out
}

// isbn13From10Body is the ISBN-13 for the first nine digits of an ISBN-10.
func isbn13From10Body(body string) string {
	b := "978" + body
	sum := 0
	for i, c := range b {
		d := int(c - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return b + string(rune('0'+(10-sum%10)%10))
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// HasAnyField says whether any provider answered with something usable.
func (m *MergedBookResult) HasAnyField() bool {
	return m != nil && (m.Title != nil || m.Authors != nil || m.ISBN13 != nil || m.ISBN10 != nil || len(m.Covers) > 0)
}
