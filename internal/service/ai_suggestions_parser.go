// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"regexp"
	"strings"
	"unicode"
)

// ParsedSuggestion is one extracted line from the AI's output. ISBN may be
// empty for read_next rows (the AI is asked to echo titles from the user's
// library, not ISBNs). Reason is trimmed and may be empty.
type ParsedSuggestion struct {
	Title  string
	ISBN   string
	Author string
	Reason string
}

// suggestionLinePattern parses lines of the shape:
//
//	1. Title — ISBN — Reason
//	1) Title - 9781234567890 - Reason goes here
//	- Title | ISBN-10 | reason text
//
// All three separators (em-dash, hyphen-dash, pipe) are accepted. Leading list
// numbering is optional. ISBN is captured only when it looks like an ISBN —
// otherwise the second field is treated as the reason (which can happen for
// read_next rows). The regex is deliberately lenient: the parser runs on model
// output, and we'd rather accept a slightly off-format line than drop it.
var listMarker = regexp.MustCompile(`^\s*(?:[-*•]|\d+[.\)])\s*`)

// ParseSuggestions extracts ParsedSuggestion rows from raw model output.
// Format expected: one suggestion per line, fields separated by —, -, or |.
// Lines that don't parse as a suggestion (blanks, headers, prose) are skipped.
func ParseSuggestions(text string) []ParsedSuggestion {
	var out []ParsedSuggestion
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		line = listMarker.ReplaceAllString(line, "")
		parts := splitSuggestionFields(line)
		if len(parts) < 2 {
			continue
		}
		p := ParsedSuggestion{Title: cleanField(parts[0])}
		if p.Title == "" {
			continue
		}
		// Second field: either ISBN or reason.
		second := cleanField(parts[1])
		if isbnLike(second) {
			p.ISBN = normalizeISBN(second)
			if len(parts) >= 3 {
				p.Reason = cleanField(strings.Join(parts[2:], " — "))
			}
		} else {
			p.Reason = second
			if len(parts) >= 3 {
				// Third-field ISBN — happens when the AI includes author in the
				// middle slot.
				third := cleanField(parts[2])
				if isbnLike(third) {
					p.Author = second
					p.Reason = ""
					p.ISBN = normalizeISBN(third)
					if len(parts) >= 4 {
						p.Reason = cleanField(strings.Join(parts[3:], " — "))
					}
				} else {
					p.Reason = third
				}
			}
		}
		out = append(out, p)
	}
	return out
}

// splitSuggestionFields splits on the first separator type it finds in the
// line so mixed separators don't confuse the parser.
func splitSuggestionFields(line string) []string {
	switch {
	case strings.Contains(line, "—"):
		return strings.Split(line, "—")
	case strings.Contains(line, "|"):
		return strings.Split(line, "|")
	case strings.Contains(line, " - "):
		return strings.Split(line, " - ")
	}
	return []string{line}
}

func cleanField(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	s = strings.TrimSpace(s)
	return s
}

// isbnLike decides whether a field looks ISBN-ish. We don't full-validate the
// checksum here — the metadata provider lookup will reject garbage.
func isbnLike(s string) bool {
	digits := 0
	for _, r := range s {
		switch {
		case unicode.IsDigit(r), r == 'X', r == 'x':
			digits++
		case r == '-', r == ' ':
			// ignore
		default:
			return false
		}
	}
	return digits == 10 || digits == 13
}

// normalizeISBN strips dashes and spaces.
func normalizeISBN(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r != '-' && r != ' ' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fuzzyTitleMatch returns true when two titles match after normalisation
// (lowercase, strip non-alphanumerics, collapse whitespace). Used to confirm
// an ISBN's returned title matches what the AI said it would be — a
// cheap-but-effective hallucination check without pulling a Levenshtein dep.
func fuzzyTitleMatch(a, b string) bool {
	na := normalizeTitle(a)
	nb := normalizeTitle(b)
	if na == "" || nb == "" {
		return false
	}
	if na == nb {
		return true
	}
	// Accept substring match either direction — "The Three-Body Problem"
	// vs "Three Body Problem" still passes.
	if strings.Contains(na, nb) || strings.Contains(nb, na) {
		return true
	}
	return false
}

// titleTokenSynonyms collapses common abbreviations to a canonical form so
// "Saga, Vol. 1" and "Saga Volume 1" compare equal. Keep this list short and
// unambiguous — single-letter entries like "v"→"volume" produce false positives.
var titleTokenSynonyms = map[string]string{
	"vol":  "volume",
	"vols": "volume",
	"ed":   "edition",
	"edn":  "edition",
	"pt":   "part",
	"bk":   "book",
}

func normalizeTitle(s string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastSpace = false
		default:
			// Any non-alphanumeric rune (space, hyphen, punctuation) collapses
			// to a single separating space. Without this "The Three-Body Problem"
			// becomes "thethreebodyproblem" and stops matching "three body problem".
			if !lastSpace && b.Len() > 0 {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	tokens := strings.Fields(b.String())
	for i, tok := range tokens {
		if canon, ok := titleTokenSynonyms[tok]; ok {
			tokens[i] = canon
		}
	}
	// Drop series-numbering words that sit directly before a digit token:
	// "Saga Volume 1" / "Saga Book 1" / "Saga #1" all reduce to "saga 1", and
	// the "#" case already falls through here because # was stripped to a
	// space. Gated on "next token is all digits" so we don't mangle titles
	// like "Book of the New Sun".
	var cleaned []string
	for i := 0; i < len(tokens); i++ {
		if i+1 < len(tokens) && isSeriesWord(tokens[i]) && isAllDigits(tokens[i+1]) {
			continue
		}
		cleaned = append(cleaned, tokens[i])
	}
	return strings.Join(cleaned, " ")
}

func isSeriesWord(s string) bool {
	switch s {
	case "volume", "book", "part", "edition", "number":
		return true
	}
	return false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// fuzzyAuthorMatch returns true when two author strings plausibly refer to the
// same person. Authors come in a lot of shapes — "Neil Gaiman", "gaiman, neil",
// "Neil Gaiman & Terry Pratchett", "Ryoko Kui" vs "Kui, Ryoko" — so we
// tokenise, lowercase, and check that every token in the shorter side appears
// on the longer side. Used by the ISBN→title fallback to reject results that
// happen to share a title but come from a different author.
func fuzzyAuthorMatch(a, b string) bool {
	ta := authorTokens(a)
	tb := authorTokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	short, long := ta, tb
	if len(tb) < len(ta) {
		short, long = tb, ta
	}
	have := make(map[string]struct{}, len(long))
	for _, t := range long {
		have[t] = struct{}{}
	}
	for _, t := range short {
		if _, ok := have[t]; !ok {
			return false
		}
	}
	return true
}

// authorTokens normalises an author string into a set of lowercase name tokens,
// dropping single-character initials (they rarely match cleanly across
// providers and tend to pull in false positives).
func authorTokens(s string) []string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	var out []string
	for _, tok := range strings.Fields(b.String()) {
		if len(tok) <= 1 {
			continue
		}
		out = append(out, tok)
	}
	return out
}
