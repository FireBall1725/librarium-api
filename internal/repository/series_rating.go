// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import "fmt"

// What a run is worth, from what its volumes are worth.
//
// Nothing rates a series. People rate books, and a series is the books, so the
// number is derived: the average of the volume averages, over the volumes
// anyone has rated at all. A run with one rated volume has that volume's
// rating; a run with none has no rating, which is different from a rating of
// nought and is why every one of these returns NULL rather than zero.
//
// The average of averages, not the average of every rating. Each volume counts
// once however many people rated it. Otherwise a single volume that five people
// loved outweighs nineteen the rest of the run never touched, and a
// twenty-volume series reports the opinion of its most-discussed book.
//
// Rounded to a whole stored point, which is half a star, for the same reason
// the book average is: an unrounded average lands somewhere different for every
// series, so a facet built on it would grow a row per series.

// seriesAvgRatingScalar is the run's rating, for a query that has `s` in scope.
//
// Averaged over raters who hold a role on a library in scope, matching the book
// average exactly. An instance can host two households that share no library,
// and one household's opinions have no business moving the other's number.
func seriesAvgRatingScalar(libraryArg string) string {
	return fmt.Sprintf(`(
        SELECT round(avg(per_book.rating))::int FROM (
            SELECT avg(ub.rating) AS rating
              FROM book_series bs_r
              JOIN user_books ub ON ub.book_id = bs_r.book_id
             WHERE bs_r.series_id = s.id
               AND ub.rating IS NOT NULL AND ub.deleted_at IS NULL
               AND EXISTS (SELECT 1 FROM user_roles ur_r
                            WHERE ur_r.user_id = ub.user_id
                              AND (ur_r.library_id IS NULL OR ur_r.library_id = ANY(%s)))
             GROUP BY ub.book_id
        ) per_book)`, libraryArg)
}

// seriesRatedBooksScalar is how many volumes of the run carry a rating.
//
// Sent beside the average because a 4 from one volume of twenty and a 4 from
// all twenty are not the same claim, and a reader deciding whether to trust the
// number needs to know which it is.
func seriesRatedBooksScalar(libraryArg string) string {
	return fmt.Sprintf(`(
        SELECT count(DISTINCT ub.book_id)::int
          FROM book_series bs_c
          JOIN user_books ub ON ub.book_id = bs_c.book_id
         WHERE bs_c.series_id = s.id
           AND ub.rating IS NOT NULL AND ub.deleted_at IS NULL
           AND EXISTS (SELECT 1 FROM user_roles ur_c
                        WHERE ur_c.user_id = ub.user_id
                          AND (ur_c.library_id IS NULL OR ur_c.library_id = ANY(%s))))`, libraryArg)
}

// seriesMyRatingScalar is the caller's own average over the run.
//
// A separate question from the one above, the same way it is on Books: "is this
// run good" and "did I like it" are two filters, not one, and in a household
// they routinely disagree.
func seriesMyRatingScalar(callerArg string) string {
	return fmt.Sprintf(`(
        SELECT round(avg(ub.rating))::int
          FROM book_series bs_m
          JOIN user_books ub ON ub.book_id = bs_m.book_id
         WHERE bs_m.series_id = s.id AND ub.user_id = %s
           AND ub.rating IS NOT NULL AND ub.deleted_at IS NULL)`, callerArg)
}
