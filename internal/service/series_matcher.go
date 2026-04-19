// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"regexp"
	"strconv"
	"strings"
)

// Matches "<separator?> <keyword?> <number>" right after a series name. The
// separator is any combo of `,:;.-–—`, the keyword is a volume/book marker
// (`#`, `vol`, `volume`, `book`, etc.), and the number accepts fractional
// positions like `1.5`.
var matchVolumeRe = regexp.MustCompile(`^\s*[,:;.\-–—]?\s*(?:#|vol\.?|volume|book|bk\.?|no\.?|ch\.?|chapter)?\s*(\d+(?:\.\d+)?)\b`)

// MatchTitleToSeries checks whether the title begins with the series name
// followed by a volume number (optionally preceded by a separator and a
// volume keyword). It returns (position, true) on a match, or (0, false)
// when no volume can be extracted.
//
// Examples:
//
//	("Bleach #1: The Death and the Strawberry", "Bleach")   → (1, true)
//	("Bleach, Vol. 3",                           "Bleach")   → (3, true)
//	("Bleach 5",                                 "Bleach")   → (5, true)
//	("Bleach: Fade to Black",                    "Bleach")   → (0, false)
//	("Bleachers",                                "Bleach")   → (0, false)
func MatchTitleToSeries(title, seriesName string) (float64, bool) {
	title = strings.TrimSpace(title)
	seriesName = strings.TrimSpace(seriesName)
	if title == "" || seriesName == "" {
		return 0, false
	}
	if len(title) < len(seriesName) {
		return 0, false
	}
	if !strings.EqualFold(title[:len(seriesName)], seriesName) {
		return 0, false
	}
	rest := title[len(seriesName):]
	if rest == "" {
		return 0, false
	}
	// Require a non-alphanumeric boundary after the series name so "Bleach"
	// doesn't also match "Bleachers".
	next := rest[0]
	if (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') || (next >= '0' && next <= '9') {
		return 0, false
	}
	m := matchVolumeRe.FindStringSubmatch(strings.ToLower(rest))
	if len(m) < 2 {
		return 0, false
	}
	pos, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return pos, true
}
