// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

// Package preflight reports what the schema-tiers migration would find in a
// database before anyone upgrades into it.
//
// The migration itself refuses rather than guessing when it meets data it
// cannot map, which is the right behaviour and arrives at the worst possible
// moment: migrations run at boot, so a refusal is a server that will not start.
// The same questions asked ahead of time turn that into a decision someone can
// make on a Sunday afternoon.
//
// It only reads. Nothing here writes, locks, or takes more than a few seconds.
package preflight

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Severity says what a finding means for an upgrade.
type Severity int

const (
	// Info is a count the migration handles mechanically. Reported so the
	// numbers can be checked against the report afterwards.
	Info Severity = iota
	// Blocking is data the migration will refuse to guess about. These have to
	// be resolved by hand first.
	Blocking
)

// Finding is one question, its answer, and what that answer means.
type Finding struct {
	Question string
	Count    int
	Severity Severity
	Meaning  string
}

type check struct {
	question string
	sql      string
	severity Severity
	// meaning is rendered with the count so it can read naturally at zero and
	// at a hundred.
	meaning func(n int) string
}

var checks = []check{
	{
		question: "works with reading state on more than one edition",
		severity: Info,
		sql: `SELECT count(*) FROM (
		        SELECT i.user_id, e.book_id
		        FROM user_book_interactions i
		        JOIN book_editions e ON e.id = i.book_edition_id
		        WHERE i.deleted_at IS NULL
		        GROUP BY i.user_id, e.book_id
		        HAVING count(*) > 1) t`,
		meaning: func(n int) string {
			if n == 0 {
				return "the collapse onto the work is a straight copy"
			}
			return "these collapse using the priority the app already applies: read beats reading beats did_not_finish"
		},
	},
	{
		question: "of those, ones that actually disagree",
		severity: Blocking,
		sql: `SELECT count(*) FROM (
		        SELECT i.user_id, e.book_id
		        FROM user_book_interactions i
		        JOIN book_editions e ON e.id = i.book_edition_id
		        WHERE i.deleted_at IS NULL
		        GROUP BY i.user_id, e.book_id
		        HAVING count(DISTINCT i.read_status) > 1
		            OR count(DISTINCT i.rating) > 1
		            OR count(DISTINCT NULLIF(i.review, '')) > 1
		            OR count(DISTINCT NULLIF(i.notes, '')) > 1) t`,
		meaning: func(n int) string {
			if n == 0 {
				return "nothing to lose"
			}
			return "a rating or review would have to be dropped; resolve these before upgrading"
		},
	},
	// user_book_interactions.read_status is a VARCHAR(32) with a default and no
	// CHECK, so an empty string has always been a legal value there. user_books
	// constrains it, and the backfill copies whatever it finds, so one of these
	// rows stops the upgrade with a constraint violation partway through the
	// migration. Reported for the value that would actually be chosen rather
	// than for every bad row, because the aggregate prefers a real status when
	// any edition of that work has one.
	{
		question: "reading records whose status is not a status",
		severity: Blocking,
		sql: `SELECT count(*) FROM (
		        SELECT (ARRAY_AGG(i.read_status ORDER BY CASE i.read_status
		                   WHEN 'read' THEN 1 WHEN 'reading' THEN 2
		                   WHEN 'did_not_finish' THEN 3 ELSE 4 END))[1] AS chosen
		          FROM user_book_interactions i
		          JOIN book_editions e ON e.id = i.book_edition_id
		         WHERE i.deleted_at IS NULL
		         GROUP BY i.user_id, e.book_id) t
		 WHERE chosen IS NULL
		    OR chosen NOT IN ('unread', 'reading', 'read', 'did_not_finish')`,
		meaning: func(n int) string {
			if n == 0 {
				return "every reading record carries a status the new table accepts"
			}
			return "the migration will stop on these: set them to 'unread' first, " +
				"UPDATE user_book_interactions SET read_status = 'unread' " +
				"WHERE read_status IS NULL OR read_status NOT IN " +
				"('unread', 'reading', 'read', 'did_not_finish')"
		},
	},
	// Deliberately counts duplicate names anywhere, not just across libraries.
	// De-scoping merges by name globally, so two rows sharing a name inside one
	// library get merged exactly like two rows spread across two. An earlier
	// version of this check asked "in more than one library" and reported clear
	// on a collection that had a same-library duplicate waiting to be merged.
	{
		question: "tag names that will be merged into one",
		severity: Info,
		sql: `SELECT count(*) FROM (
		        SELECT lower(name) FROM tags
		        GROUP BY lower(name) HAVING count(*) > 1) t`,
		meaning: func(n int) string {
			if n == 0 {
				return "every tag name is already unique"
			}
			return "each name collapses to a single tag carrying every book the duplicates had"
		},
	},
	{
		question: "series names that will be merged into one",
		severity: Info,
		sql: `SELECT count(*) FROM (
		        SELECT lower(name) FROM series WHERE deleted_at IS NULL
		        GROUP BY lower(name) HAVING count(*) > 1) t`,
		meaning: func(n int) string {
			if n == 0 {
				return "every series name is already unique"
			}
			return "each name collapses to one series; check the volume lists agree before upgrading"
		},
	},
	{
		question: "works held with no edition recorded",
		severity: Info,
		sql: `SELECT count(*) FROM library_books lb
		      WHERE lb.deleted_at IS NULL
		        AND NOT EXISTS (
		          SELECT 1 FROM library_book_editions lbe
		          JOIN book_editions e ON e.id = lbe.book_edition_id
		          WHERE lbe.library_id = lb.library_id
		            AND e.book_id = lb.book_id
		            AND lbe.deleted_at IS NULL)`,
		meaning: func(n int) string { return "become copies with a null edition, which is a supported state" },
	},
	{
		question: "loans where more than one copy could be the one lent",
		severity: Blocking,
		sql: `SELECT count(*) FROM loans l
		      WHERE l.deleted_at IS NULL
		        AND (SELECT count(*) FROM library_book_editions lbe
		             JOIN book_editions e ON e.id = lbe.book_edition_id
		             WHERE lbe.library_id = l.library_id
		               AND e.book_id = l.book_id
		               AND lbe.deleted_at IS NULL) > 1`,
		meaning: func(n int) string {
			if n == 0 {
				return "every loan has exactly one candidate copy"
			}
			return "the migration would have to pick; say which copy was lent before upgrading"
		},
	},
	{
		question: "holdings recording more than one copy",
		severity: Info,
		sql:      `SELECT count(*) FROM library_book_editions WHERE deleted_at IS NULL AND copy_count > 1`,
		meaning: func(n int) string {
			if n == 0 {
				return "no holding has to be split into separate copy rows"
			}
			return "each becomes that many copy rows, all sharing the attributes the holding had"
		},
	},
	{
		question: "ISBN-13 values claimed by more than one edition",
		severity: Blocking,
		sql: `SELECT count(*) FROM (
		        SELECT isbn_13 FROM book_editions
		        WHERE isbn_13 ~ '^[0-9]{13}$'
		        GROUP BY isbn_13 HAVING count(*) > 1) t`,
		meaning: func(n int) string {
			if n == 0 {
				return "the unique constraint on identifiers can be added safely"
			}
			return "identifiers become unique per scheme, so these have to be reconciled first"
		},
	},
	{
		question: "language codes not in BCP 47",
		severity: Info,
		sql:      `SELECT count(*) FROM book_editions WHERE language ~ '^[a-z]{3}$'`,
		meaning:  func(n int) string { return "mapped mechanically, e.g. eng to en, jpn to ja" },
	},
	{
		question: "publication dates that are really year-only",
		severity: Info,
		sql: `SELECT count(*) FROM book_editions
		      WHERE publish_date IS NOT NULL
		        AND extract(month from publish_date) = 1
		        AND extract(day from publish_date) = 1`,
		meaning: func(n int) string {
			return "recorded as year precision; a book genuinely published on 1 January is misread, and a refresh can correct it"
		},
	},
	{
		question: "series links with no position",
		severity: Blocking,
		sql:      `SELECT count(*) FROM book_series WHERE position IS NULL`,
		meaning: func(n int) string {
			if n == 0 {
				return "every link maps to a volume slot without guessing"
			}
			return "these have no slot to map to; give them a position before upgrading"
		},
	},
}

// Run executes every check and returns the findings in report order.
func Run(ctx context.Context, pool *pgxpool.Pool) ([]Finding, error) {
	out := make([]Finding, 0, len(checks))
	for _, c := range checks {
		var n int
		if err := pool.QueryRow(ctx, c.sql).Scan(&n); err != nil {
			return nil, fmt.Errorf("checking %q: %w", c.question, err)
		}
		out = append(out, Finding{
			Question: c.question,
			Count:    n,
			Severity: c.severity,
			Meaning:  c.meaning(n),
		})
	}
	return out, nil
}

// Blocked reports whether any finding would stop the migration.
func Blocked(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == Blocking && f.Count > 0 {
			return true
		}
	}
	return false
}

// Report writes the findings as a table meant to be read in a terminal.
func Report(w io.Writer, findings []Finding) {
	line(w, "Librarium schema preflight")
	line(w, strings.Repeat("=", 72))
	line(w, "")

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, f := range findings {
		marker := " "
		if f.Severity == Blocking && f.Count > 0 {
			marker = "!"
		}
		_, _ = fmt.Fprintf(tw, "%s %6d\t%s\t%s\n", marker, f.Count, f.Question, f.Meaning)
	}
	_ = tw.Flush()

	line(w, "")
	if Blocked(findings) {
		line(w, "BLOCKED. The lines marked ! have to be resolved before upgrading;")
		line(w, "the migration refuses rather than guessing about them.")
		return
	}
	line(w, "Clear. Nothing here would stop the migration.")
	line(w, "Take a backup anyway: the contract phase that drops the old tables is one-way.")
}

// line writes one line of the report.
//
// The error is dropped on purpose and in one place rather than nine: the writer
// is stdout, a failed write there means the operator's terminal is gone, and
// there is nowhere left to report that to.
func line(w io.Writer, s string) {
	_, _ = fmt.Fprintln(w, s)
}
