// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import "fmt"

// A book's rating is what the people who can see it think of it, together.
//
// Each rating is still stored per person: user_books is keyed (user_id,
// book_id) and nothing here changes that. What changed is which of those
// numbers a reader is shown and searches by, because "is this book good" is a
// question about the book and "did I like it" is a question about the reader.
// Both are askable; they are two filters, not one.
//
// Rounded to a whole stored point, which is a half star. An average is
// fractional and every book lands on a different one, so an unrounded average
// could not be a facet at all: the rail would grow a row per book. Rounding
// keeps the ten values the checkboxes and the "and up" presets already work on.
//
// Averaged over raters who hold a role on a library in scope. An instance can
// host two households that share no library, and one household's opinions have
// no business moving the other's average.
func avgRatingCTE(libraryArg string) string {
	return fmt.Sprintf(`
    SELECT ub.book_id, round(avg(ub.rating))::int AS rating, count(*)::int AS raters
      FROM user_books ub
     WHERE ub.rating IS NOT NULL AND ub.deleted_at IS NULL
       AND EXISTS (SELECT 1 FROM user_roles ur
                    WHERE ur.user_id = ub.user_id
                      AND (ur.library_id IS NULL OR ur.library_id = ANY(%s)))
     GROUP BY ub.book_id`, libraryArg)
}

// avgRatingScalar is the same answer for one book, for the list's WHERE clause.
//
// A scalar subquery rather than a join, for the reason every other filter here
// is one: joining would multiply a book by its raters and inflate the count.
func avgRatingScalar(libraryArg string) string {
	return fmt.Sprintf(`(
        SELECT round(avg(ub2.rating))::int
          FROM user_books ub2
         WHERE ub2.book_id = b.id AND ub2.rating IS NOT NULL AND ub2.deleted_at IS NULL
           AND EXISTS (SELECT 1 FROM user_roles ur2
                        WHERE ur2.user_id = ub2.user_id
                          AND (ur2.library_id IS NULL OR ur2.library_id = ANY(%s))))`, libraryArg)
}
