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

// LibraryBookRepo manages the library_books and library_book_editions
// junction tables — i.e., which libraries hold which works (and copy
// counts at the edition level).
type LibraryBookRepo struct {
	db *pgxpool.Pool
}

func NewLibraryBookRepo(db *pgxpool.Pool) *LibraryBookRepo {
	return &LibraryBookRepo{db: db}
}

// AddBookToLibrary inserts a library_books row if one doesn't already
// exist for the (library, book) pair. Idempotent — re-adding a book
// that's already in the library is a no-op.
func (r *LibraryBookRepo) AddBookToLibrary(ctx context.Context, tx pgx.Tx, libraryID, bookID uuid.UUID, addedBy *uuid.UUID) error {
	const q = `
		INSERT INTO library_books (library_id, book_id, added_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (library_id, book_id) DO NOTHING`
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

// RemoveBookFromLibrary drops the library_books row for the given
// (library, book). Returns ErrNotFound if no such row exists.
func (r *LibraryBookRepo) RemoveBookFromLibrary(ctx context.Context, libraryID, bookID uuid.UUID) error {
	result, err := r.db.Exec(ctx,
		`DELETE FROM library_books WHERE library_id = $1 AND book_id = $2`,
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

// IsBookInLibrary returns true if the (library, book) pair exists in
// library_books.
func (r *LibraryBookRepo) IsBookInLibrary(ctx context.Context, libraryID, bookID uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM library_books WHERE library_id = $1 AND book_id = $2)`,
		libraryID, bookID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking book in library: %w", err)
	}
	return exists, nil
}

// LibrariesForBook returns lightweight references to every library that
// holds the given book (via the library_books junction).
func (r *LibraryBookRepo) LibrariesForBook(ctx context.Context, bookID uuid.UUID) ([]models.BookLibraryRef, error) {
	const q = `
		SELECT l.id, l.name
		FROM library_books lb
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

// FindLibraryBook returns the junction row for a (library, book) pair,
// or ErrNotFound.
func (r *LibraryBookRepo) FindLibraryBook(ctx context.Context, libraryID, bookID uuid.UUID) (*models.LibraryBook, error) {
	const q = `
		SELECT id, library_id, book_id, added_by, added_at
		FROM library_books
		WHERE library_id = $1 AND book_id = $2`
	var (
		lb         models.LibraryBook
		pgID       pgtype.UUID
		pgLibID    pgtype.UUID
		pgBookID   pgtype.UUID
		pgAddedBy  pgtype.UUID
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

// SetEditionCopyCount sets the copy count for an edition in a specific
// library. If copyCount is 0, the junction row is deleted. Upserts
// otherwise.
func (r *LibraryBookRepo) SetEditionCopyCount(ctx context.Context, tx pgx.Tx, libraryID, editionID uuid.UUID, copyCount int, acquiredAt *any) error {
	if copyCount <= 0 {
		q := `DELETE FROM library_book_editions WHERE library_id = $1 AND book_edition_id = $2`
		var err error
		if tx != nil {
			_, err = tx.Exec(ctx, q, libraryID, editionID)
		} else {
			_, err = r.db.Exec(ctx, q, libraryID, editionID)
		}
		return err
	}

	const q = `
		INSERT INTO library_book_editions (library_id, book_edition_id, copy_count, acquired_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (library_id, book_edition_id)
		DO UPDATE SET copy_count = EXCLUDED.copy_count,
		              acquired_at = COALESCE(EXCLUDED.acquired_at, library_book_editions.acquired_at)`
	var acq any
	if acquiredAt != nil {
		acq = *acquiredAt
	}
	var err error
	if tx != nil {
		_, err = tx.Exec(ctx, q, libraryID, editionID, copyCount, acq)
	} else {
		_, err = r.db.Exec(ctx, q, libraryID, editionID, copyCount, acq)
	}
	if err != nil {
		return fmt.Errorf("setting edition copy count: %w", err)
	}
	return nil
}

// GetEditionCopyCount returns the copy count for an edition in a
// specific library. Returns 0 if the junction row doesn't exist.
func (r *LibraryBookRepo) GetEditionCopyCount(ctx context.Context, libraryID, editionID uuid.UUID) (int, error) {
	const q = `
		SELECT COALESCE((
			SELECT copy_count FROM library_book_editions
			WHERE library_id = $1 AND book_edition_id = $2
		), 0)`
	var count int
	err := r.db.QueryRow(ctx, q, libraryID, editionID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("getting edition copy count: %w", err)
	}
	return count, nil
}
