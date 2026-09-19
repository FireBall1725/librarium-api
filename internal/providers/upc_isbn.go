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

// SplitUPCAddon splits a UPC-A with a 5-digit add-on into the six-digit
// company prefix and the add-on. It takes the 18-digit form a camera gives,
// the UPC as a 13-digit EAN with a leading zero.
func SplitUPCAddon(code string) (company, addon string, ok bool) {
	if len(code) == 18 && code[0] == '0' {
		code = code[1:]
	}
	if len(code) != 17 || !allDigits(code) {
		return "", "", false
	}
	return code[:6], code[12:], true
}

// ISBNsFromUPCAddon returns the ISBN-13s a UPC-A with a 5-digit add-on could
// stand for: the checked prefixes first, then learned (ISBN prefixes this
// instance picked up from real books, see migration 43). Empty when there's
// no add-on or no prefix is known for the publisher.
func ISBNsFromUPCAddon(code string, learned ...string) []string {
	company, addon, ok := SplitUPCAddon(code)
	if !ok {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, prefix := range append(append([]string{}, upcISBNPrefixes[company]...), learned...) {
		body := prefix + addon
		if len(body) != 9 || !allDigits(body) || seen[prefix] {
			continue
		}
		seen[prefix] = true
		out = append(out, isbn13From10Body(body))
	}
	return out
}

// LearnableUPCPrefix says whether a back-cover scan and an ISBN belong to the
// same book by the add-on rule: the add-on is digits 5 to 9 of the ISBN-10.
// If so it returns the company and ISBN prefix to remember. Only 978 ISBNs
// have an ISBN-10 to match against.
func LearnableUPCPrefix(code, isbn13 string) (company, isbnPrefix string, ok bool) {
	company, addon, ok := SplitUPCAddon(code)
	if !ok || len(isbn13) != 13 || !allDigits(isbn13) || isbn13[:3] != "978" {
		return "", "", false
	}
	body := isbn13[3:12]
	if body[4:] != addon || isbn13From10Body(body) != isbn13 {
		return "", "", false
	}
	return company, body[:4], true
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
