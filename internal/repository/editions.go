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

type EditionRepo struct {
	db *pgxpool.Pool
}

func NewEditionRepo(db *pgxpool.Pool) *EditionRepo {
	return &EditionRepo{db: db}
}

// ─── Book editions ────────────────────────────────────────────────────────────

const editionColumns = `
	id, book_id, format, COALESCE(language,''), COALESCE(edition_name,''),
	COALESCE(narrator,''), COALESCE(publisher,''), publish_date,
	COALESCE(isbn_10,''), COALESCE(isbn_13,''), COALESCE(description,''),
	duration_seconds, page_count, is_primary, created_at, updated_at,
	narrator_contributor_id,
	(SELECT name FROM contributors WHERE id = narrator_contributor_id)
`

// beEditionColumns is editionColumns with every column prefixed by the "be"
// alias, for use in queries that JOIN books (which also has id, description,
// created_at, updated_at) and would otherwise produce "column reference is
// ambiguous" errors.
const beEditionColumns = `
	be.id, be.book_id, be.format, COALESCE(be.language,''), COALESCE(be.edition_name,''),
	COALESCE(be.narrator,''), COALESCE(be.publisher,''), be.publish_date,
	COALESCE(be.isbn_10,''), COALESCE(be.isbn_13,''), COALESCE(be.description,''),
	be.duration_seconds, be.page_count, be.is_primary, be.created_at, be.updated_at,
	be.narrator_contributor_id,
	(SELECT name FROM contributors WHERE id = be.narrator_contributor_id)
`

func (r *EditionRepo) ListByBook(ctx context.Context, bookID uuid.UUID) ([]*models.BookEdition, error) {
	q := `SELECT ` + editionColumns + `FROM book_editions WHERE book_id = $1 ORDER BY is_primary DESC, created_at ASC`
	rows, err := r.db.Query(ctx, q, bookID)
	if err != nil {
		return nil, fmt.Errorf("listing editions: %w", err)
	}
	defer rows.Close()

	var out []*models.BookEdition
	for rows.Next() {
		e, err := scanEdition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *EditionRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.BookEdition, error) {
	q := `SELECT ` + editionColumns + `FROM book_editions WHERE id = $1`
	e, err := scanEdition(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding edition: %w", err)
	}
	return e, nil
}

func (r *EditionRepo) Create(ctx context.Context, tx pgx.Tx, id, bookID uuid.UUID, format, language, editionName, narrator, publisher string, publishDate any, isbn10, isbn13, description string, durationSeconds, pageCount any, isPrimary bool, narratorContributorID any) error {
	const q = `
		INSERT INTO book_editions
			(id, book_id, format, language, edition_name, narrator, publisher, publish_date, isbn_10, isbn_13, description, duration_seconds, page_count, is_primary, narrator_contributor_id)
		VALUES
			($1, $2, $3, NULLIF($4,''), NULLIF($5,''), NULLIF($6,''), NULLIF($7,''), $8, NULLIF($9,''), NULLIF($10,''), NULLIF($11,''), $12, $13, $14, $15)`
	_, err := tx.Exec(ctx, q, id, bookID, format, language, editionName, narrator, publisher, publishDate, isbn10, isbn13, description, durationSeconds, pageCount, isPrimary, narratorContributorID)
	if err != nil {
		return fmt.Errorf("inserting edition: %w", err)
	}
	return nil
}

func (r *EditionRepo) Update(ctx context.Context, tx pgx.Tx, id uuid.UUID, format, language, editionName, narrator, publisher string, publishDate any, isbn10, isbn13, description string, durationSeconds, pageCount any, isPrimary bool, narratorContributorID any) error {
	const q = `
		UPDATE book_editions
		SET format                   = $2,
		    language                 = NULLIF($3, ''),
		    edition_name             = NULLIF($4, ''),
		    narrator                 = NULLIF($5, ''),
		    publisher                = NULLIF($6, ''),
		    publish_date             = $7,
		    isbn_10                  = NULLIF($8, ''),
		    isbn_13                  = NULLIF($9, ''),
		    description              = NULLIF($10, ''),
		    duration_seconds         = $11,
		    page_count               = $12,
		    is_primary               = $13,
		    narrator_contributor_id  = $14
		WHERE id = $1`
	_, err := tx.Exec(ctx, q, id, format, language, editionName, narrator, publisher, publishDate, isbn10, isbn13, description, durationSeconds, pageCount, isPrimary, narratorContributorID)
	if err != nil {
		return fmt.Errorf("updating edition: %w", err)
	}
	return nil
}

func (r *EditionRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `DELETE FROM book_editions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting edition: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanEdition(s scanner) (*models.BookEdition, error) {
	var (
		pgID                    pgtype.UUID
		pgBookID                pgtype.UUID
		pgPubDate               pgtype.Date
		pgDuration              pgtype.Int4
		pgPageCount             pgtype.Int4
		pgNarratorContributorID pgtype.UUID
		pgNarratorContribName   pgtype.Text
		e                       models.BookEdition
	)
	err := s.Scan(
		&pgID, &pgBookID, &e.Format, &e.Language, &e.EditionName,
		&e.Narrator, &e.Publisher, &pgPubDate,
		&e.ISBN10, &e.ISBN13, &e.Description,
		&pgDuration, &pgPageCount, &e.IsPrimary, &e.CreatedAt, &e.UpdatedAt,
		&pgNarratorContributorID, &pgNarratorContribName,
	)
	if err != nil {
		return nil, err
	}
	e.ID = uuid.UUID(pgID.Bytes)
	e.BookID = uuid.UUID(pgBookID.Bytes)
	if pgPubDate.Valid {
		t := pgPubDate.Time
		e.PublishDate = &t
	}
	if pgDuration.Valid {
		v := int(pgDuration.Int32)
		e.DurationSeconds = &v
	}
	if pgPageCount.Valid {
		v := int(pgPageCount.Int32)
		e.PageCount = &v
	}
	if pgNarratorContributorID.Valid {
		id := uuid.UUID(pgNarratorContributorID.Bytes)
		e.NarratorContributorID = &id
	}
	if pgNarratorContribName.Valid {
		e.NarratorContributorName = pgNarratorContribName.String
	}
	return &e, nil
}

// ListMissingFiles returns all ebook/audiobook editions in a library that have no file attached.
func (r *EditionRepo) ListMissingFiles(ctx context.Context, libraryID uuid.UUID) ([]*models.BookEdition, error) {
	q := `SELECT ` + beEditionColumns + `
		FROM book_editions be
		JOIN books b ON b.id = be.book_id
		JOIN library_books lb ON lb.book_id = b.id
		WHERE lb.library_id = $1
		  AND be.format IN ('ebook','digital','audiobook')
		  AND NOT EXISTS (SELECT 1 FROM edition_files ef WHERE ef.edition_id = be.id)
		ORDER BY b.title, be.format`
	rows, err := r.db.Query(ctx, q, libraryID)
	if err != nil {
		return nil, fmt.Errorf("listing editions missing files: %w", err)
	}
	defer rows.Close()
	var out []*models.BookEdition
	for rows.Next() {
		e, err := scanEdition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FindByISBN returns the edition (globally, regardless of library) whose
// isbn_10 or isbn_13 matches the given value. Returns ErrNotFound if none
// match. Callers that need library scoping can check library_book_editions
// afterwards.
func (r *EditionRepo) FindByISBN(ctx context.Context, isbn string) (*models.BookEdition, error) {
	q := `SELECT ` + editionColumns + `
		FROM book_editions
		WHERE isbn_10 = $1 OR isbn_13 = $1
		ORDER BY created_at ASC
		LIMIT 1`
	e, err := scanEdition(r.db.QueryRow(ctx, q, isbn))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding edition by isbn: %w", err)
	}
	return e, nil
}

// FindByISBNInLibrary returns the edition with the given ISBN, but only if
// the given library holds it via library_book_editions. Returns ErrNotFound
// if the edition doesn't exist or isn't held by that library.
func (r *EditionRepo) FindByISBNInLibrary(ctx context.Context, libraryID uuid.UUID, isbn string) (*models.BookEdition, error) {
	q := `SELECT ` + beEditionColumns + `
		FROM book_editions be
		JOIN library_book_editions lbe ON lbe.book_edition_id = be.id
		WHERE lbe.library_id = $1 AND (be.isbn_10 = $2 OR be.isbn_13 = $2)
		LIMIT 1`
	e, err := scanEdition(r.db.QueryRow(ctx, q, libraryID, isbn))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding edition by isbn in library: %w", err)
	}
	return e, nil
}

// IncrementCopyCount bumps the copy count for an edition in a specific
// library. Upserts — if the (library, edition) row doesn't exist, creates
// it with copy_count = 1.
func (r *EditionRepo) IncrementCopyCount(ctx context.Context, libraryID, editionID uuid.UUID) error {
	const q = `
		INSERT INTO library_book_editions (library_id, book_edition_id, copy_count)
		VALUES ($1, $2, 1)
		ON CONFLICT (library_id, book_edition_id)
		DO UPDATE SET copy_count = library_book_editions.copy_count + 1`
	_, err := r.db.Exec(ctx, q, libraryID, editionID)
	if err != nil {
		return fmt.Errorf("incrementing copy count: %w", err)
	}
	return nil
}

// ─── User interactions ────────────────────────────────────────────────────────

const interactionColumns = `
	id, user_id, book_edition_id, read_status, rating, COALESCE(notes,''), COALESCE(review,''),
	date_started, date_finished, is_favorite, reread_count, created_at, updated_at
`

func (r *EditionRepo) GetInteraction(ctx context.Context, userID, editionID uuid.UUID) (*models.UserBookInteraction, error) {
	q := `SELECT ` + interactionColumns + `FROM user_book_interactions WHERE user_id = $1 AND book_edition_id = $2`
	i, err := scanInteraction(r.db.QueryRow(ctx, q, userID, editionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting interaction: %w", err)
	}
	return i, nil
}

func (r *EditionRepo) UpsertInteraction(ctx context.Context, userID, editionID uuid.UUID, readStatus string, rating any, notes, review string, dateStarted, dateFinished any, isFavorite bool) (*models.UserBookInteraction, error) {
	const q = `
		INSERT INTO user_book_interactions
			(id, user_id, book_edition_id, read_status, rating, notes, review, date_started, date_finished, is_favorite)
		VALUES
			($1, $2, $3, $4, $5, NULLIF($6,''), NULLIF($7,''), $8, $9, $10)
		ON CONFLICT (user_id, book_edition_id) DO UPDATE
		SET read_status   = EXCLUDED.read_status,
		    rating        = EXCLUDED.rating,
		    notes         = EXCLUDED.notes,
		    review        = EXCLUDED.review,
		    date_started  = EXCLUDED.date_started,
		    date_finished = EXCLUDED.date_finished,
		    is_favorite   = EXCLUDED.is_favorite,
		    updated_at    = NOW()
		RETURNING ` + interactionColumns

	i, err := scanInteraction(r.db.QueryRow(ctx, q, uuid.New(), userID, editionID, readStatus, rating, notes, review, dateStarted, dateFinished, isFavorite))
	if err != nil {
		return nil, fmt.Errorf("upserting interaction: %w", err)
	}
	return i, nil
}

func (r *EditionRepo) DeleteInteraction(ctx context.Context, userID, editionID uuid.UUID) error {
	result, err := r.db.Exec(ctx,
		`DELETE FROM user_book_interactions WHERE user_id = $1 AND book_edition_id = $2`,
		userID, editionID,
	)
	if err != nil {
		return fmt.Errorf("deleting interaction: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanInteraction(s scanner) (*models.UserBookInteraction, error) {
	var (
		pgID          pgtype.UUID
		pgUserID      pgtype.UUID
		pgEditionID   pgtype.UUID
		pgRating      pgtype.Int4
		pgDateStarted pgtype.Date
		pgDateFinished pgtype.Date
		i             models.UserBookInteraction
	)
	err := s.Scan(
		&pgID, &pgUserID, &pgEditionID,
		&i.ReadStatus, &pgRating, &i.Notes, &i.Review,
		&pgDateStarted, &pgDateFinished,
		&i.IsFavorite, &i.RereadCount, &i.CreatedAt, &i.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	i.ID = uuid.UUID(pgID.Bytes)
	i.UserID = uuid.UUID(pgUserID.Bytes)
	i.BookEditionID = uuid.UUID(pgEditionID.Bytes)
	if pgRating.Valid {
		v := int(pgRating.Int32)
		i.Rating = &v
	}
	if pgDateStarted.Valid {
		t := pgDateStarted.Time
		i.DateStarted = &t
	}
	if pgDateFinished.Valid {
		t := pgDateFinished.Time
		i.DateFinished = &t
	}
	return &i, nil
}
