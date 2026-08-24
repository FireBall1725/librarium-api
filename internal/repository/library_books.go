// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LibraryBookRepo answers which libraries hold which works.
//
// Writes go to copies, one row per physical object. Reads go through the
// held_books view, which collapses those copies back to one row per work so a
// list does not show a book twice because someone owns two of it.
//
// The old library_books and library_book_editions tables still exist and still
// hold what they held before the tiers migration. Nothing writes to them now,
// and contract drops them once this shape has proven itself.
type LibraryBookRepo struct {
	db *pgxpool.Pool
}

func NewLibraryBookRepo(db *pgxpool.Pool) *LibraryBookRepo {
	return &LibraryBookRepo{db: db}
}

// AddBookToLibrary records that a library holds a work, by creating a copy.
//
// Idempotent in the sense that matters: adding a book already in the library
// does nothing rather than quietly giving you a second copy. Someone who
// genuinely owns two says so through the copies endpoint, where they can also
// say which one is signed. A junction row could not tell those cases apart at
// all, which is why it stopped being a junction row.
//
// The copy carries no edition, condition or location. That is the ordinary
// case: adding a book is one action, and nobody meets the word "copy" until
// they own two.
func (r *LibraryBookRepo) AddBookToLibrary(ctx context.Context, tx pgx.Tx, libraryID, bookID uuid.UUID, addedBy *uuid.UUID) error {
	const q = `
		INSERT INTO copies (library_id, book_id, acquired_by)
		SELECT $1, $2, $3
		 WHERE NOT EXISTS (
		     SELECT 1 FROM copies
		      WHERE library_id = $1 AND book_id = $2 AND deleted_at IS NULL)`
	var err error
	if tx != nil {
		_, err = tx.Exec(ctx, q, libraryID, bookID, addedBy)
	} else {
		_, err = r.db.Exec(ctx, q, libraryID, bookID, addedBy)
	}
	if err != nil {
		return fmt.Errorf("adding book to library: %w", err)
	}
	return nil
}

// RemoveBookFromLibrary stops a library holding a work, by soft-deleting every
// copy of it there. Returns ErrNotFound if the library did not hold it.
//
// Soft rather than hard: a copy carries condition, provenance and price that
// someone typed, and losing that to a misclick is the expensive mistake. The
// old junction row held nothing worth keeping, so deleting it outright was
// fine; a copy is not.
func (r *LibraryBookRepo) RemoveBookFromLibrary(ctx context.Context, libraryID, bookID uuid.UUID) error {
	result, err := r.db.Exec(ctx,
		`UPDATE copies SET deleted_at = NOW(), updated_at = NOW()
		  WHERE library_id = $1 AND book_id = $2 AND deleted_at IS NULL`,
		libraryID, bookID,
	)
	if err != nil {
		return fmt.Errorf("removing book from library: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// IsBookInLibrary reports whether a library holds a work.
func (r *LibraryBookRepo) IsBookInLibrary(ctx context.Context, libraryID, bookID uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM held_books WHERE library_id = $1 AND book_id = $2)`,
		libraryID, bookID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking book in library: %w", err)
	}
	return exists, nil
}

// LibrariesForBook returns lightweight references to every library that
// holds the given book.
func (r *LibraryBookRepo) LibrariesForBook(ctx context.Context, bookID uuid.UUID) ([]models.BookLibraryRef, error) {
	const q = `
		SELECT l.id, l.name
		FROM held_books lb
		JOIN libraries l ON l.id = lb.library_id
		WHERE lb.book_id = $1
		ORDER BY lb.added_at ASC`
	rows, err := r.db.Query(ctx, q, bookID)
	if err != nil {
		return nil, fmt.Errorf("listing libraries for book: %w", err)
	}
	defer rows.Close()

	var out []models.BookLibraryRef
	for rows.Next() {
		var pgID pgtype.UUID
		var name string
		if err := rows.Scan(&pgID, &name); err != nil {
			return nil, fmt.Errorf("scanning library ref: %w", err)
		}
		out = append(out, models.BookLibraryRef{ID: uuid.UUID(pgID.Bytes), Name: name})
	}
	return out, rows.Err()
}

// FindLibraryBook returns the holding for a (library, book) pair, or
// ErrNotFound. The id is the earliest copy's; a holding is now a set of copies
// and the thing worth addressing individually is a copy.
func (r *LibraryBookRepo) FindLibraryBook(ctx context.Context, libraryID, bookID uuid.UUID) (*models.LibraryBook, error) {
	const q = `
		SELECT id, library_id, book_id, added_by, added_at
		FROM held_books
		WHERE library_id = $1 AND book_id = $2`
	var (
		lb        models.LibraryBook
		pgID      pgtype.UUID
		pgLibID   pgtype.UUID
		pgBookID  pgtype.UUID
		pgAddedBy pgtype.UUID
	)
	err := r.db.QueryRow(ctx, q, libraryID, bookID).Scan(&pgID, &pgLibID, &pgBookID, &pgAddedBy, &lb.AddedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding library book: %w", err)
	}
	lb.ID = uuid.UUID(pgID.Bytes)
	lb.LibraryID = uuid.UUID(pgLibID.Bytes)
	lb.BookID = uuid.UUID(pgBookID.Bytes)
	if pgAddedBy.Valid {
		id := uuid.UUID(pgAddedBy.Bytes)
		lb.AddedBy = &id
	}
	return &lb, nil
}

// SetEditionCopyCount makes a library hold exactly copyCount copies of an
// edition, adding or retiring rows to get there.
//
// A count is no longer a column, so this reconciles rather than assigns. That
// distinction is the whole point of the change: setting the count to 1 when
// there are two must retire one specific object, and the one it keeps should be
// the one someone bothered to describe.
//
// So the copies retired first are the ones carrying the least: no condition,
// not signed, no note, no price. A signed first printing survives a careless
// edit to the number in a form.
func (r *LibraryBookRepo) SetEditionCopyCount(ctx context.Context, tx pgx.Tx, libraryID, editionID uuid.UUID, copyCount int, acquiredAt *any) error {
	exec := func(q string, args ...any) error {
		var err error
		if tx != nil {
			_, err = tx.Exec(ctx, q, args...)
		} else {
			_, err = r.db.Exec(ctx, q, args...)
		}
		return err
	}

	if copyCount <= 0 {
		// Soft, because a copy carries things someone typed. The old junction
		// row held nothing worth keeping; this does.
		return exec(`
			UPDATE copies SET deleted_at = NOW(), updated_at = NOW()
			 WHERE library_id = $1 AND edition_id = $2 AND deleted_at IS NULL`,
			libraryID, editionID)
	}

	var acq any
	if acquiredAt != nil {
		acq = *acquiredAt
	}

	// Retire the surplus, least-described first. describedness orders by how
	// much a person put into the row, so the plain ones go before the signed one.
	if err := exec(`
		UPDATE copies SET deleted_at = NOW(), updated_at = NOW()
		 WHERE id IN (
		     SELECT id FROM copies
		      WHERE library_id = $1 AND edition_id = $2 AND deleted_at IS NULL
		      ORDER BY (is_signed::int
		                + (condition IS NOT NULL)::int
		                + (COALESCE(notes, '') <> '')::int
		                + (price_minor IS NOT NULL)::int) ASC,
		               created_at DESC, id DESC
		     OFFSET $3)`, libraryID, editionID, copyCount); err != nil {
		return fmt.Errorf("retiring surplus copies: %w", err)
	}

	// Then top up to the requested number. generate_series makes the shortfall
	// one statement rather than a loop.
	if err := exec(`
		INSERT INTO copies (library_id, book_id, edition_id, acquired_at)
		SELECT $1, e.book_id, $2, $4::date
		  FROM book_editions e
		  CROSS JOIN generate_series(1, GREATEST($3 - (
		         SELECT count(*) FROM copies
		          WHERE library_id = $1 AND edition_id = $2 AND deleted_at IS NULL), 0)) AS n
		 WHERE e.id = $2`, libraryID, editionID, copyCount, acq); err != nil {
		return fmt.Errorf("adding copies: %w", err)
	}
	return nil
}

// GetEditionCopyCount counts how many copies of an edition a library holds.
// A count rather than a stored number, so it cannot drift from the rows.
func (r *LibraryBookRepo) GetEditionCopyCount(ctx context.Context, libraryID, editionID uuid.UUID) (int, error) {
	const q = `
		SELECT count(*) FROM copies
		 WHERE library_id = $1 AND edition_id = $2 AND deleted_at IS NULL`
	var count int
	err := r.db.QueryRow(ctx, q, libraryID, editionID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("getting edition copy count: %w", err)
	}
	return count, nil
}
