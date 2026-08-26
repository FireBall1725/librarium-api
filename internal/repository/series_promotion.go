// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Promotion: turning a volume a provider knows about into a book the
// collection can talk about.
//
// A gap in the ownership facet is a book recorded against one of your series
// that no library holds. The detection has always worked and has always had
// nothing to detect, because a volume in series_volumes is not a book. This is
// the step that makes it one: a book row with no edition and no copy, joined
// to the series at the volume's position, which is exactly the shape gap looks
// for.
//
// The book is deliberately thin. A volume nobody holds has a title, a position
// and sometimes a date, and inventing an edition for it would be claiming to
// know a printing that has not been seen.

// PromotionResult reports what a run did, per series, so a job can say
// something more useful than "finished".
type PromotionResult struct {
	Promoted int
	// Matched counts volumes joined to a book that was already in the series
	// at that position rather than to a new one. This is the common case on
	// first run against a real collection and is not a no-op: it is what stops
	// the next run promoting a duplicate.
	Matched int
}

// PromoteVolumes creates a book for every volume of a series that does not have
// one, and links the volumes that do.
//
// Idempotent by construction: a volume with book_id set is left alone, and the
// unique index on that column means a concurrent second run loses the race
// rather than writing a duplicate.
func (r *SeriesRepo) PromoteVolumes(ctx context.Context, seriesID uuid.UUID) (PromotionResult, error) {
	var out PromotionResult

	// The series' media type comes from what is already in it, because a
	// provider does not say whether a run is manga or a graphic novel and the
	// column is NOT NULL. A series with nothing in it yet has nothing to copy,
	// so it is left for the reader to seed rather than guessed at.
	var (
		mediaTypeID pgtype.UUID
		seriesName  string
	)
	err := r.db.QueryRow(ctx, `
		SELECT b.media_type_id, max(s.name)
		  FROM book_series bs
		  JOIN books b ON b.id = bs.book_id
		  JOIN series s ON s.id = bs.series_id
		 WHERE bs.series_id = $1
		 GROUP BY b.media_type_id
		 ORDER BY count(*) DESC
		 LIMIT 1`, seriesID).Scan(&mediaTypeID, &seriesName)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return out, ErrNoSeedBook
	case err != nil:
		return out, fmt.Errorf("finding the series' media type: %w", err)
	}

	// A run can know how long it is without knowing what is in it.
	//
	// total_count comes from the provider and is often the only thing it says
	// about the volumes nobody has: Absolute Boyfriend reports seven volumes
	// and lists six, so volume seven exists in the world, is missing from the
	// shelf, and had nowhere to be recorded. Filling the gap here rather than
	// inventing a book directly keeps one bridge: everything becomes a volume
	// first, and promotion is the only thing that turns a volume into a book.
	//
	// Whole positions only, and only inside the count. Half positions are side
	// stories a publisher's total does not describe, and guessing at them would
	// invent volumes that were never announced.
	if _, err := r.db.Exec(ctx, `
		INSERT INTO series_volumes (series_id, position)
		SELECT $1, p
		  FROM series s, generate_series(1, COALESCE(s.total_count, 0)) AS p
		 WHERE s.id = $1
		   AND NOT EXISTS (SELECT 1 FROM series_volumes sv
		                    WHERE sv.series_id = $1 AND sv.position = p)
		   AND NOT EXISTS (SELECT 1 FROM book_series bs
		                    WHERE bs.series_id = $1 AND bs.position = p)
		ON CONFLICT (series_id, position) DO NOTHING`, seriesID); err != nil {
		return out, fmt.Errorf("recording the volumes a total implies: %w", err)
	}

	// And the other direction, because a total can shrink.
	//
	// A count is one number from a provider and it can be wrong or revised. The
	// inference above is only safe if it is reversible: otherwise a run that
	// said seven and turned out to be six keeps a phantom volume seven forever,
	// with nothing corroborating it the way a provider-listed volume is
	// corroborated.
	//
	// Only rows this inference created are eligible, which is what the empty
	// title and identifiers mean: a volume a provider actually described stays
	// whatever the count later says, because the provider saw it and the count
	// is only a summary. And only after their books have been demoted, which is
	// guarded, so a placeholder somebody has since acquired or filed on a list
	// keeps its volume rather than being orphaned.
	var stale []uuid.UUID
	staleRows, err := r.db.Query(ctx, `
		SELECT sv.id FROM series_volumes sv JOIN series s ON s.id = sv.series_id
		 WHERE sv.series_id = $1
		   AND s.total_count IS NOT NULL AND sv.position > s.total_count
		   AND COALESCE(sv.title, '') = '' AND COALESCE(sv.external_id, '') = ''
		   AND sv.external_ids = '{}'::jsonb`, seriesID)
	if err != nil {
		return out, fmt.Errorf("looking for volumes a shrunken total dropped: %w", err)
	}
	for staleRows.Next() {
		var id pgtype.UUID
		if err := staleRows.Scan(&id); err != nil {
			staleRows.Close()
			return out, err
		}
		stale = append(stale, uuid.UUID(id.Bytes))
	}
	staleRows.Close()
	if err := staleRows.Err(); err != nil {
		return out, err
	}
	if len(stale) > 0 {
		if _, err := r.DemoteUnheldVolumes(ctx, stale); err != nil {
			return out, err
		}
		if _, err := r.db.Exec(ctx, `
			DELETE FROM series_volumes sv
			 WHERE sv.id = ANY($1) AND sv.book_id IS NULL`, stale); err != nil {
			return out, fmt.Errorf("removing volumes a shrunken total dropped: %w", err)
		}
	}

	rows, err := r.db.Query(ctx, `
		SELECT sv.id, sv.position, COALESCE(sv.title, '')
		  FROM series_volumes sv
		 WHERE sv.series_id = $1 AND sv.book_id IS NULL
		 ORDER BY sv.position`, seriesID)
	if err != nil {
		return out, fmt.Errorf("listing unpromoted volumes: %w", err)
	}
	type pending struct {
		id       uuid.UUID
		position float64
		title    string
	}
	var todo []pending
	for rows.Next() {
		var (
			pgID pgtype.UUID
			p    pending
		)
		if err := rows.Scan(&pgID, &p.position, &p.title); err != nil {
			rows.Close()
			return out, err
		}
		p.id = uuid.UUID(pgID.Bytes)
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	for _, v := range todo {
		matched, err := r.promoteOne(
			ctx, seriesID, seriesName, v.id, v.position, v.title, uuid.UUID(mediaTypeID.Bytes))
		switch {
		case err != nil:
			return out, err
		case matched:
			out.Matched++
		default:
			out.Promoted++
		}
	}
	return out, nil
}

// ErrNoSeedBook is returned when a series holds no book, so there is nothing to
// take a media type from.
//
// Promoting into an empty series would mean inventing the answer to "what kind
// of thing is this run", and getting it wrong puts a shelf of manga in with the
// cookbooks.
var ErrNoSeedBook = errors.New("the series has no book to take a media type from")

// promoteOne handles a single volume in its own transaction.
//
// Per volume rather than per series: a provider list of eighty volumes where
// one row is malformed should leave the other seventy-nine promoted, which is
// the same call Sync already makes about the data it writes.
func (r *SeriesRepo) promoteOne(
	ctx context.Context,
	seriesID uuid.UUID,
	seriesName string,
	volumeID uuid.UUID,
	position float64,
	title string,
	mediaTypeID uuid.UUID,
) (matched bool, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("starting the promotion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Already a book at this position in this series? Then the volume is that
	// book, and saying so is the whole point: it is what stops the next run
	// promoting a duplicate beside something the reader already owns.
	//
	// Position within the series is the match, not the title. Titles disagree
	// across providers and editions in ways nobody can normalise ("Vol. 3",
	// "#3: Good Intentions", the same volume under its Japanese name), while
	// the position is the one thing a run agrees on with itself.
	var existing pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT bs.book_id
		  FROM book_series bs
		 WHERE bs.series_id = $1 AND bs.position = $2
		   AND NOT EXISTS (
		       SELECT 1 FROM series_volumes sv WHERE sv.book_id = bs.book_id
		   )
		 LIMIT 1`, seriesID, position).Scan(&existing)
	switch {
	case err == nil:
		if _, err := tx.Exec(ctx,
			`UPDATE series_volumes SET book_id = $1, updated_at = NOW() WHERE id = $2`,
			uuid.UUID(existing.Bytes), volumeID); err != nil {
			return false, fmt.Errorf("linking volume %s: %w", volumeID, err)
		}
		return true, tx.Commit(ctx)
	case !errors.Is(err, pgx.ErrNoRows):
		return false, fmt.Errorf("looking for a book at position %g: %w", position, err)
	}

	// Most volumes arrive without a title. Of the 448 on record in a real
	// collection, 189 have none: the providers that answer series_volumes sync
	// positions and release dates and often nothing else. Refusing to promote
	// those would leave two fifths of every run permanently invisible, which is
	// the exact failure this whole piece of work exists to end.
	//
	// So the title is derived rather than invented: the series name and the
	// volume number, which is what the volume is and which is how the books
	// already in these collections are named ("Chainsaw Man #19"). Nothing is
	// claimed that the position does not already say.
	if title == "" {
		title = fmt.Sprintf("%s #%s", seriesName, formatPosition(position))
	}

	bookID := uuid.New()
	if _, err := tx.Exec(ctx,
		`INSERT INTO books (id, title, media_type_id) VALUES ($1, $2, $3)`,
		bookID, title, mediaTypeID); err != nil {
		return false, fmt.Errorf("creating the book for volume %s: %w", volumeID, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO book_series (book_id, series_id, position) VALUES ($1, $2, $3)`,
		bookID, seriesID, position); err != nil {
		return false, fmt.Errorf("putting the new book in the series: %w", err)
	}
	// Who wrote it. A numbered volume of a run has the run's author, and the
	// card shows a contributor line, so leaving it blank makes a real missing
	// volume look like a broken row.
	//
	// Only where every book already in the series agrees. Unanimity is the
	// whole guard: an anthology with a different artist per volume, or a run
	// that changed hands, disagrees with itself and gets nothing rather than a
	// guess put in somebody's catalogue under a real person's name.
	if _, err := tx.Exec(ctx, `
		INSERT INTO book_contributors (book_id, contributor_id, role, display_order)
		SELECT $1, bc.contributor_id, bc.role, min(bc.display_order)
		  FROM book_series bs
		  JOIN book_contributors bc ON bc.book_id = bs.book_id
		 WHERE bs.series_id = $2 AND bs.book_id <> $1
		 GROUP BY bc.contributor_id, bc.role
		HAVING count(DISTINCT bs.book_id) = (
		    SELECT count(*) FROM book_series x
		     WHERE x.series_id = $2 AND x.book_id <> $1
		)`, bookID, seriesID); err != nil {
		return false, fmt.Errorf("carrying the series' contributors over: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE series_volumes SET book_id = $1, updated_at = NOW() WHERE id = $2`,
		bookID, volumeID); err != nil {
		return false, fmt.Errorf("recording the promotion: %w", err)
	}
	return false, tx.Commit(ctx)
}

// DemoteUnheldVolumes removes the books a promotion invented and nobody has
// since done anything with.
//
// Called when a series is deleted or when a sync finds a volume no longer
// exists. The guards are the point: a promoted book that has since been
// acquired, rated, reviewed, given an edition, or put on somebody's list is no
// longer a placeholder, and deleting it would take real work with it.
func (r *SeriesRepo) DemoteUnheldVolumes(ctx context.Context, volumeIDs []uuid.UUID) (int, error) {
	if len(volumeIDs) == 0 {
		return 0, nil
	}
	tag, err := r.db.Exec(ctx, `
		DELETE FROM books b
		 WHERE b.id IN (
		     SELECT sv.book_id FROM series_volumes sv
		      WHERE sv.id = ANY($1) AND sv.book_id IS NOT NULL
		 )
		   AND NOT EXISTS (SELECT 1 FROM copies c WHERE c.book_id = b.id)
		   AND NOT EXISTS (SELECT 1 FROM book_editions e WHERE e.book_id = b.id)
		   AND NOT EXISTS (SELECT 1 FROM user_books ub WHERE ub.book_id = b.id)
		   AND NOT EXISTS (SELECT 1 FROM list_books lb WHERE lb.book_id = b.id)
		   AND NOT EXISTS (SELECT 1 FROM wishlist_items w WHERE w.book_id = b.id)
		   AND NOT EXISTS (SELECT 1 FROM book_contents bc
		                    WHERE bc.container_id = b.id OR bc.contained_id = b.id)`,
		volumeIDs)
	if err != nil {
		return 0, fmt.Errorf("removing promoted books: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// formatPosition writes 3 rather than 3.0, and keeps 4.5. Half positions are
// real: side stories and specials are numbered that way, which is why the
// column is a float rather than an integer.
func formatPosition(pos float64) string {
	if pos == float64(int64(pos)) {
		return strconv.FormatInt(int64(pos), 10)
	}
	return strconv.FormatFloat(pos, 'f', -1, 64)
}
