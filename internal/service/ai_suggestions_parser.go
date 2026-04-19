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
	return strings.TrimSpace(b.String())
}
