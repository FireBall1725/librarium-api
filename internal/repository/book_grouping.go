// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725
//
// Books with each series collapsed into one entry.
//
// Browsing a shelf by title means scrolling past thirty-four volumes of one
// series to reach the next thing. Grouped, a run and a standalone book are
// peers: one entry each.
//
// Four decisions are baked into the SQL here, because each one has a defensible
// alternative and the wrong choice is invisible until someone notices a number
// that does not add up:
//
//  1. A group appears when ANY of its books match the filter, and the count it
//     reports is how many MATCHED, not how big the series is. Filtering to
//     "reading" and seeing "Berserk 34" when one volume is in progress would
//     describe the shelf rather than the filter. Both numbers are returned so
//     the client can say "1 of 34".
//  2. Paging is over entries, not books. A page of 50 entries is not 50 books,
//     so the book total is returned alongside and the client has to show both.
//     The facet rail still counts books, and a rail disagreeing with the list
//     beside it is the failure this is written to avoid.
//  3. A book in two series is counted once, under the series whose name sorts
//     first. Showing it twice would make the entry counts stop summing to the
//     book total.
//  4. Sorting is by the entry's label. A group has no author and no publish
//     date, so the other sort columns have nothing to sort a group by.
//
// The filter itself is buildBookFilter, unchanged and shared with the ungrouped
// list and the facet counts. Reimplementing it here is how the three drift.

package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/fireball1725/librarium-api/internal/models"
)

// SeriesGroup is a run of books shown as one entry.
type SeriesGroup struct {
	ID   uuid.UUID
	Name string
	// Matched is how many books in this series match the current filter.
	Matched int
	// Owned is how many the caller's libraries hold in total, filter ignored,
	// so the client can say "3 of 34" rather than just "3".
	Owned int
	// Read is how many of the owned books the caller has finished.
	Read int
	// TotalCount is the series' own published length where it is known, which
	// is what "missing volumes" is measured against.
	TotalCount *int
	// CoverBookID is the volume whose cover represents the run: the lowest
	// position that actually has one.
	CoverBookID    *uuid.UUID
	CoverUpdatedAt *time.Time
}

// GroupedEntry is either a series or a single book. Exactly one is non-nil.
type GroupedEntry struct {
	Series *SeriesGroup
	Book   *models.Book
}

// seriesKeyExpr picks the one series a book is grouped under.
//
// Deterministic on name then id so the same book lands in the same group on
// every query. A book in no series gets NULL and stays a standalone entry.
const seriesKeyExpr = `(
	SELECT bs.series_id
	FROM book_series bs
	JOIN series s_k ON s_k.id = bs.series_id
	WHERE bs.book_id = b.id
	ORDER BY lower(s_k.name), s_k.id
	LIMIT 1
)`

// ListGroupedBySeries returns one page of entries plus the entry total and the
// book total the entries were collapsed from.
func (r *BookRepo) ListGroupedBySeries(
	ctx context.Context, libraryIDs []uuid.UUID, opts ListBooksOpts,
) (entries []GroupedEntry, entryTotal, bookTotal int, err error) {
	if len(libraryIDs) == 0 {
		return nil, 0, 0, nil
	}
	if opts.PerPage <= 0 || opts.PerPage > 200 {
		opts.PerPage = 25
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}
	offset := (opts.Page - 1) * opts.PerPage

	f := r.buildBookFilter(libraryIDs, opts)
	args := append([]any{}, f.args...)
	argIdx := f.nextArg

	// The filtered set, each book tagged with the series it groups under.
	matched := `
	matched AS (
		SELECT b.id AS book_id, ` + seriesKeyExpr + ` AS series_id
		FROM books b
		JOIN media_types mt ON mt.id = b.media_type_id
		` + f.scopeJoin + f.where + `
	)`

	// One row per entry: a series, or a book that is in none.
	entriesCTE := `
	entries AS (
		SELECT 'series' AS kind, m.series_id AS id, s.name AS label, count(*)::int AS matched
		FROM matched m
		JOIN series s ON s.id = m.series_id
		WHERE m.series_id IS NOT NULL
		GROUP BY m.series_id, s.name
		UNION ALL
		SELECT 'book', m.book_id, b.title, 1
		FROM matched m
		JOIN books b ON b.id = m.book_id
		WHERE m.series_id IS NULL
	)`

	sortDir := "ASC"
	if opts.SortDir == "desc" {
		sortDir = "DESC"
	}

	countQ := "WITH " + matched + ", " + entriesCTE + `
	SELECT (SELECT count(*) FROM entries), (SELECT count(*) FROM matched)`
	if err := r.db.QueryRow(ctx, countQ, args...).Scan(&entryTotal, &bookTotal); err != nil {
		return nil, 0, 0, fmt.Errorf("counting grouped entries: %w", err)
	}

	args = append(args, opts.PerPage, offset)
	pageQ := "WITH " + matched + ", " + entriesCTE + fmt.Sprintf(`
	SELECT kind, id, matched FROM entries
	ORDER BY natural_sort_key(label) %s, label %s
	LIMIT $%d OFFSET $%d`, sortDir, sortDir, argIdx, argIdx+1)

	rows, err := r.db.Query(ctx, pageQ, args...)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("listing grouped entries: %w", err)
	}

	type ref struct {
		kind    string
		id      uuid.UUID
		matched int
	}
	var refs []ref
	var seriesIDs, bookIDs []uuid.UUID
	for rows.Next() {
		var x ref
		if err := rows.Scan(&x.kind, &x.id, &x.matched); err != nil {
			rows.Close()
			return nil, 0, 0, err
		}
		refs = append(refs, x)
		if x.kind == "series" {
			seriesIDs = append(seriesIDs, x.id)
		} else {
			bookIDs = append(bookIDs, x.id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}

	groups, err := r.seriesGroups(ctx, libraryIDs, opts.CallerID, seriesIDs)
	if err != nil {
		return nil, 0, 0, err
	}
	books, err := r.booksByID(ctx, libraryIDs, opts.CallerID, bookIDs)
	if err != nil {
		return nil, 0, 0, err
	}

	// Rebuilt in the page's order. The hydration queries return their own
	// order, and using it would re-sort the page the database already sorted.
	for _, x := range refs {
		if x.kind == "series" {
			g, ok := groups[x.id]
			if !ok {
				continue
			}
			g.Matched = x.matched
			entries = append(entries, GroupedEntry{Series: g})
			continue
		}
		if b, ok := books[x.id]; ok {
			entries = append(entries, GroupedEntry{Book: b})
		}
	}
	return entries, entryTotal, bookTotal, nil
}

// seriesGroups loads the per-series numbers the entry row shows.
//
// Owned and Read ignore the filter on purpose: they describe the run, so that
// a filtered view can say "3 of 34" instead of leaving the reader to guess how
// much of the series they are not looking at.
func (r *BookRepo) seriesGroups(
	ctx context.Context, libraryIDs []uuid.UUID, callerID uuid.UUID, ids []uuid.UUID,
) (map[uuid.UUID]*SeriesGroup, error) {
	out := make(map[uuid.UUID]*SeriesGroup, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	readExpr := "0"
	args := []any{ids, libraryIDs}
	if callerID != uuid.Nil {
		args = append(args, callerID)
		readExpr = `(
			SELECT count(DISTINCT bs_r.book_id)::int
			FROM book_series bs_r
			JOIN held_books lb_r ON lb_r.book_id = bs_r.book_id
				AND lb_r.library_id = ANY($2) AND lb_r.deleted_at IS NULL
			JOIN book_editions be_r ON be_r.book_id = bs_r.book_id
			JOIN user_book_interactions ubi_r ON ubi_r.book_edition_id = be_r.id
				AND ubi_r.user_id = $3 AND ubi_r.read_status = 'read'
			WHERE bs_r.series_id = s.id
		)`
	}

	q := `
	SELECT s.id, s.name, s.total_count,
		(
			SELECT count(DISTINCT bs_o.book_id)::int
			FROM book_series bs_o
			JOIN held_books lb_o ON lb_o.book_id = bs_o.book_id
				AND lb_o.library_id = ANY($2) AND lb_o.deleted_at IS NULL
			WHERE bs_o.series_id = s.id
		) AS owned,
		` + readExpr + ` AS read_count,
		(
			SELECT bs_c.book_id
			FROM book_series bs_c
			JOIN held_books lb_c ON lb_c.book_id = bs_c.book_id
				AND lb_c.library_id = ANY($2) AND lb_c.deleted_at IS NULL
			WHERE bs_c.series_id = s.id
			  AND EXISTS (
				SELECT 1 FROM cover_images ci
				WHERE ci.entity_type = 'book' AND ci.entity_id = bs_c.book_id
				  AND ci.is_primary = true
			  )
			ORDER BY bs_c.position NULLS LAST
			LIMIT 1
		) AS cover_book_id
	FROM series s
	WHERE s.id = ANY($1)`

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("loading series groups: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var g SeriesGroup
		var total *int
		var coverID *uuid.UUID
		if err := rows.Scan(&g.ID, &g.Name, &total, &g.Owned, &g.Read, &coverID); err != nil {
			return nil, err
		}
		g.TotalCount = total
		g.CoverBookID = coverID
		out[g.ID] = &g
	}
	return out, rows.Err()
}

// booksByID hydrates the standalone entries through the same select the
// ungrouped list uses, so a book looks identical either way.
func (r *BookRepo) booksByID(
	ctx context.Context, libraryIDs []uuid.UUID, callerID uuid.UUID, ids []uuid.UUID,
) (map[uuid.UUID]*models.Book, error) {
	out := make(map[uuid.UUID]*models.Book, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	args := []any{libraryIDs, ids}
	sel := booksSelect(0, 1, true)
	if callerID != uuid.Nil {
		args = append(args, callerID)
		sel = booksSelect(3, 1, true)
	}

	rows, err := r.db.Query(ctx, sel+" WHERE b.id = ANY($2)", args...)
	if err != nil {
		return nil, fmt.Errorf("loading grouped books: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, err
		}
		out[b.ID] = b
	}
	return out, rows.Err()
}
