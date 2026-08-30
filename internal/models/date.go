// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package models

import (
	"regexp"
	"strings"
	"time"
)

// DatePrecision says how much of a publication date the source actually gave.
// It is stored beside the date because a year-only date is written as 1
// January, and nothing about the stored value distinguishes that from a book
// published on 1 January. book_editions constrains the pair: the precision is
// NULL exactly when the date is.
type DatePrecision string

const (
	DatePrecisionYear  DatePrecision = "year"
	DatePrecisionMonth DatePrecision = "month"
	DatePrecisionDay   DatePrecision = "day"
)

// ParseFlexDate tries several date formats and returns the parsed time along
// with how much of it the input actually specified.
// Accepts: YYYY-MM-DD, YYYY-MM, YYYY, "January 2006", "January 2, 2006",
// "Jan 2, 2006", "Jan 2006", "2 January 2006", "2 Jan 2006".
//
// The precision has to travel with the date because the two are stored
// together and constrained together: book_editions carries a
// publish_date_precision that is NULL exactly when publish_date is, and a
// year-only date is written as 1 January, which no later reader can tell apart
// from a book genuinely published on 1 January.
func ParseFlexDate(s string) (time.Time, DatePrecision, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, "", false
	}

	// Exact formats, most specific first, each paired with what it pins down.
	for _, f := range []struct {
		layout    string
		precision DatePrecision
	}{
		{"2006-01-02", DatePrecisionDay},
		{"2006-01", DatePrecisionMonth},
		{"2006", DatePrecisionYear},
	} {
		if t, err := time.Parse(f.layout, s); err == nil {
			return t, f.precision, true
		}
	}

	// Year-only bare number
	if reYear.MatchString(s) {
		if t, err := time.Parse("2006", s); err == nil {
			return t, DatePrecisionYear, true
		}
	}

	// "Month DD, YYYY" or "Month D YYYY" (full or abbreviated)
	if m := reFullDate.FindStringSubmatch(s); m != nil {
		day := m[2]
		if len(day) == 1 {
			day = "0" + day
		}
		for _, mfmt := range []string{"January", "Jan"} {
			if t, err := time.Parse(mfmt+" 02 2006", m[1]+" "+day+" "+m[3]); err == nil {
				return t, DatePrecisionDay, true
			}
		}
	}

	// "Month YYYY" (full or abbreviated)
	if m := reMonYear.FindStringSubmatch(s); m != nil {
		for _, mfmt := range []string{"January 2006", "Jan 2006"} {
			if t, err := time.Parse(mfmt, m[1]+" "+m[2]); err == nil {
				return t, DatePrecisionMonth, true
			}
		}
	}

	// "D Month YYYY" or "D Mon YYYY" (day-first European)
	if m := reDayFirst.FindStringSubmatch(s); m != nil {
		day := m[1]
		if len(day) == 1 {
			day = "0" + day
		}
		for _, mfmt := range []string{"January", "Jan"} {
			if t, err := time.Parse("02 "+mfmt+" 2006", day+" "+m[2]+" "+m[3]); err == nil {
				return t, DatePrecisionDay, true
			}
		}
	}

	return time.Time{}, "", false
}

var (
	reYear     = regexp.MustCompile(`^\d{4}$`)
	reFullDate = regexp.MustCompile(`^([A-Za-z]+)\s+(\d{1,2}),?\s+(\d{4})$`)
	reMonYear  = regexp.MustCompile(`^([A-Za-z]+)\s+(\d{4})$`)
	reDayFirst = regexp.MustCompile(`^(\d{1,2})\s+([A-Za-z]+)\s+(\d{4})$`)
)
