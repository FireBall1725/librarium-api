// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"regexp"
	"strconv"
	"strings"
)

// volumeSuffixKeywordRe strips a trailing volume marker (with keyword) from a
// title: "Series Name Vol. 3", "Series, Volume 5", "Series #7: Sub", etc.
// The alphabetic keywords require a space or punctuation boundary before them
// so "Yearbook 2024" does not match the `book` keyword.
var volumeSuffixKeywordRe = regexp.MustCompile(`^(.+?)(?:(?:\s+|[,:;.\-–—]+\s*)(?:vol\.?|volume|book|bk\.?|no\.?|ch\.?|chapter)|\s*#)\s*(\d+(?:\.\d+)?)\b.*$`)

// volumeSuffixBareRe strips a trailing bare number with no keyword:
// "Series Name 3", "Series Name, 5". Rejects titles that are only numbers.
var volumeSuffixBareRe = regexp.MustCompile(`^(.+?\S)\s*,?\s+(\d+(?:\.\d+)?)\s*$`)

// maxBareVolumePosition caps the position for a bare-number match so a
// year-stamped title like "Yearbook 2024" doesn't get grouped as volume 2024.
const maxBareVolumePosition = 300

// ExtractSeriesBase inspects a book title and tries to split it into a
// "series base name" plus a volume position. Returns (base, position, true)
// when a volume marker or trailing number is detected; otherwise ok is false.
//
// Examples:
//
//	"Naruto, Vol. 3"           → ("Naruto", 3, true)
//	"One Piece Vol. 12"        → ("One Piece", 12, true)
//	"Bleach #5: Subtitle"      → ("Bleach", 5, true)
//	"Akira 4"                  → ("Akira", 4, true)
//	"The Stand"                → ("", 0, false)
//	"1984"                     → ("", 0, false)
//	"Yearbook 2024"            → ("", 0, false)  // position > cap
func ExtractSeriesBase(title string) (string, float64, bool) {
	t := strings.TrimSpace(title)
	if t == "" {
		return "", 0, false
	}
	lower := strings.ToLower(t)

	// Keyword match (preferred — high confidence).
	if m := volumeSuffixKeywordRe.FindStringSubmatch(lower); len(m) == 3 {
		if pos, err := strconv.ParseFloat(m[2], 64); err == nil {
			baseEnd := len(m[1])
			base := trimBase(t[:baseEnd])
			if base != "" && hasLetter(base) {
				return base, pos, true
			}
		}
	}

	// Bare-number fallback (lower confidence — capped).
	if m := volumeSuffixBareRe.FindStringSubmatch(lower); len(m) == 3 {
		if pos, err := strconv.ParseFloat(m[2], 64); err == nil {
			if pos > maxBareVolumePosition {
				return "", 0, false
			}
			baseEnd := len(m[1])
			base := trimBase(t[:baseEnd])
			if base != "" && hasLetter(base) {
				return base, pos, true
			}
		}
	}

	return "", 0, false
}

// NormalizeSeriesKey returns a stable lowercase grouping key for a base name.
func NormalizeSeriesKey(base string) string {
	s := strings.ToLower(strings.TrimSpace(base))
	return strings.Join(strings.Fields(s), " ")
}

func trimBase(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), " \t,:;.-")
}

func hasLetter(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}
