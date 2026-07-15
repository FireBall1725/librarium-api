// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import "fmt"

const (
	asciiNormNoSpace = `[^a-z0-9]`
	asciiNormSpace   = `[^a-z0-9 ]`
)

// titleContainsSQL matches title via ILIKE and, when the term retains Latin/
// numeric characters after ASCII normalisation, a punctuation-insensitive
// fallback (e.g. "spiderman" matches "Spider-Man"). The fallback is skipped
// when normalisation yields an empty string so Cyrillic/CJK queries do not
// degenerate to ILIKE '%%' and match every row.
func titleContainsSQL(argIdx int, normPattern string) string {
	return fmt.Sprintf(
		`(b.title ILIKE '%%' || $%[1]d || '%%' `+
			`OR (`+
			`regexp_replace(lower($%[2]d), '%[3]s', '', 'g') <> '' `+
			`AND regexp_replace(lower(b.title), '%[3]s', '', 'g') `+
			`ILIKE '%%' || regexp_replace(lower($%[2]d), '%[3]s', '', 'g') || '%%'`+
			`) `+
			`OR EXISTS (`+
			`SELECT 1 FROM book_contributors bc_q `+
			`JOIN contributors c_q ON c_q.id = bc_q.contributor_id `+
			`WHERE bc_q.book_id = b.id AND c_q.name ILIKE '%%' || $%[4]d || '%%'`+
			`))`,
		argIdx,
		argIdx+1,
		normPattern,
		argIdx+2,
	)
}

// titlePhraseSQL matches an exact substring in title or contributor name.
func titlePhraseSQL(argIdx int) string {
	return fmt.Sprintf(
		`(b.title ILIKE '%%' || $%[1]d || '%%' `+
			`OR EXISTS (`+
			`SELECT 1 FROM book_contributors bc_q `+
			`JOIN contributors c_q ON c_q.id = bc_q.contributor_id `+
			`WHERE bc_q.book_id = b.id AND c_q.name ILIKE '%%' || $%[2]d || '%%'`+
			`))`,
		argIdx,
		argIdx+1,
	)
}
