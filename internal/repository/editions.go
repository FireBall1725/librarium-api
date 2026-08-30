// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
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

// Create inserts an edition. publishPrecision travels with publishDate because
// editions_precision_needs_date requires the pair to be NULL together: writing a
// date without saying how much of it the source gave violates the constraint,
// and the insert fails rather than storing a date nobody can interpret.
func (r *EditionRepo) Create(ctx context.Context, tx pgx.Tx, id, bookID uuid.UUID, format, language, editionName, narrator, publisher string, publishDate any, publishPrecision models.DatePrecision, isbn10, isbn13, description string, durationSeconds, pageCount any, isPrimary bool, narratorContributorID any) error {
	const q = `
		INSERT INTO book_editions
			(id, book_id, format, language, edition_name, narrator, publisher, publish_date, publish_date_precision, isbn_10, isbn_13, description, duration_seconds, page_count, is_primary, narrator_contributor_id)
		VALUES
			($1, $2, $3, NULLIF($4,''), NULLIF($5,''), NULLIF($6,''), NULLIF($7,''), $8, CASE WHEN $8::date IS NULL THEN NULL ELSE $9 END, NULLIF($10,''), NULLIF($11,''), NULLIF($12,''), $13, $14, $15, $16)`
	_, err := tx.Exec(ctx, q, id, bookID, format, language, editionName, narrator, publisher, publishDate, precisionOrDay(publishPrecision), isbn10, isbn13, description, durationSeconds, pageCount, isPrimary, narratorContributorID)
	if err != nil {
		return fmt.Errorf("inserting edition: %w", err)
	}
	return nil
}

// Update rewrites an edition. The precision is derived from the date on every
// write rather than left alone, because clearing a date on a row that had one
// would otherwise leave a precision behind and violate
// editions_precision_needs_date.
func (r *EditionRepo) Update(ctx context.Context, tx pgx.Tx, id uuid.UUID, format, language, editionName, narrator, publisher string, publishDate any, publishPrecision models.DatePrecision, isbn10, isbn13, description string, durationSeconds, pageCount any, isPrimary bool, narratorContributorID any) error {
	const q = `
		UPDATE book_editions
		SET format                   = $2,
		    language                 = NULLIF($3, ''),
		    edition_name             = NULLIF($4, ''),
		    narrator                 = NULLIF($5, ''),
		    publisher                = NULLIF($6, ''),
		    publish_date             = $7,
		    publish_date_precision   = CASE WHEN $7::date IS NULL THEN NULL ELSE $8 END,
		    isbn_10                  = NULLIF($9, ''),
		    isbn_13                  = NULLIF($10, ''),
		    description              = NULLIF($11, ''),
		    duration_seconds         = $12,
		    page_count               = $13,
		    is_primary               = $14,
		    narrator_contributor_id  = $15
		WHERE id = $1`
	_, err := tx.Exec(ctx, q, id, format, language, editionName, narrator, publisher, publishDate, precisionOrDay(publishPrecision), isbn10, isbn13, description, durationSeconds, pageCount, isPrimary, narratorContributorID)
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
		JOIN held_books lb ON lb.book_id = b.id
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
// match. Callers that need library scoping can check for a copy
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

// FindByISBNInLibrary returns the edition with the given ISBN, but only if the
// given library holds a copy of it. Returns ErrNotFound otherwise.
//
// EXISTS rather than a join: a library holding three copies of a printing must
// still return one edition, and a join would return it three times.
//
// The ISBN comparison still reads the isbn_10 and isbn_13 columns rather than
// edition_identifiers. Both are populated and agree, and moving this lookup is
// part of retiring those columns rather than of moving holdings.
func (r *EditionRepo) FindByISBNInLibrary(ctx context.Context, libraryID uuid.UUID, isbn string) (*models.BookEdition, error) {
	q := `SELECT ` + beEditionColumns + `
		FROM book_editions be
		WHERE (be.isbn_10 = $2 OR be.isbn_13 = $2)
		  AND EXISTS (SELECT 1 FROM copies c
		               WHERE c.edition_id = be.id AND c.library_id = $1
		                 AND c.deleted_at IS NULL)
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

// IncrementCopyCount records one more copy of an edition in a library.
//
// Now literally what the name says: it adds a row rather than raising a number.
// The new copy carries no condition, price or location, which is right for the
// path that calls this (a scan that found a duplicate), and whoever wants to
// say the second one is signed can do that against the copy afterwards.
func (r *EditionRepo) IncrementCopyCount(ctx context.Context, libraryID, editionID uuid.UUID) error {
	const q = `
		INSERT INTO copies (library_id, book_id, edition_id)
		SELECT $1, e.book_id, e.id FROM book_editions e WHERE e.id = $2`
	_, err := r.db.Exec(ctx, q, libraryID, editionID)
	if err != nil {
		return fmt.Errorf("incrementing copy count: %w", err)
	}
	return nil
}

// ─── User interactions ────────────────────────────────────────────────────────

// ─── Reading state, via the edition-shaped API ────────────────────────────
//
// These keep their old signatures because the old routes still use them, and
// they now read and write user_books like everything else. That is the point:
// if this path still wrote user_book_interactions, marking a book read through
// the old endpoint would be invisible to the new one, and the two would drift
// apart the moment a client moved.
//
// The edition in the signature is resolved to its work. Callers pass an edition
// because that is the shape the old URL has; the answer belongs to the book.
//
// Dates and progress live in reading_sessions now, so a call carrying them
// writes a session as well as the verdict.

// bookOfEdition resolves the work an edition belongs to.
func (r *EditionRepo) bookOfEdition(ctx context.Context, editionID uuid.UUID) (uuid.UUID, error) {
	var bookID uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT book_id FROM book_editions WHERE id = $1`, editionID).Scan(&bookID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolving edition's book: %w", err)
	}
	return bookID, nil
}

// readInteraction builds the edition-shaped view of a work's reading state.
// The session supplies the dates, which is where a date belongs: it describes a
// pass through the book, not the verdict on it.
func (r *EditionRepo) readInteraction(ctx context.Context, userID, editionID, bookID uuid.UUID) (*models.UserBookInteraction, error) {
	const q = `
		SELECT ub.id, ub.user_id, ub.read_status, ub.rating, COALESCE(ub.notes,''), COALESCE(ub.review,''),
		       ub.is_favorite, ub.created_at, ub.updated_at,
		       (SELECT min(rs.started_at) FROM reading_sessions rs
		         WHERE rs.user_id = ub.user_id AND rs.book_id = ub.book_id),
		       (SELECT max(rs.finished_at) FROM reading_sessions rs
		         WHERE rs.user_id = ub.user_id AND rs.book_id = ub.book_id),
		       (SELECT count(*) FROM reading_sessions rs
		         WHERE rs.user_id = ub.user_id AND rs.book_id = ub.book_id
		           AND rs.status = 'finished')
		  FROM user_books ub
		 WHERE ub.user_id = $1 AND ub.book_id = $2 AND ub.deleted_at IS NULL`

	var i models.UserBookInteraction
	var sessions int
	// The id is load-bearing rather than decoration: the sync outbox addresses
	// ops by it, so returning the zero UUID here would have every offline edit
	// aimed at a row that does not exist, acknowledged as not_found, and
	// dropped by the client.
	err := r.db.QueryRow(ctx, q, userID, bookID).Scan(
		&i.ID, &i.UserID, &i.ReadStatus, &i.Rating, &i.Notes, &i.Review,
		&i.IsFavorite, &i.CreatedAt, &i.UpdatedAt,
		&i.DateStarted, &i.DateFinished, &sessions)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading interaction: %w", err)
	}

	i.BookEditionID = editionID
	// A reread is a second finished session, so the count is derived rather
	// than stored. Sessions before the migration could not be reconstructed, so
	// an old reread_count of 3 reads as 1 here; that loss is in the release
	// notes rather than hidden.
	if sessions > 1 {
		i.RereadCount = sessions - 1
	}
	return &i, nil
}

// GetInteraction returns the caller's reading state for the work this edition
// belongs to.
func (r *EditionRepo) GetInteraction(ctx context.Context, userID, editionID uuid.UUID) (*models.UserBookInteraction, error) {
	bookID, err := r.bookOfEdition(ctx, editionID)
	if err != nil {
		return nil, err
	}
	return r.readInteraction(ctx, userID, editionID, bookID)
}

// UpsertInteraction replaces the caller's reading state for the work.
func (r *EditionRepo) UpsertInteraction(ctx context.Context, userID, editionID uuid.UUID, readStatus string, rating any, notes, review string, dateStarted, dateFinished any, isFavorite bool, progress []byte) (*models.UserBookInteraction, error) {
	bookID, err := r.bookOfEdition(ctx, editionID)
	if err != nil {
		return nil, err
	}
	if readStatus == "" {
		readStatus = "unread"
	}

	const q = `
		INSERT INTO user_books (user_id, book_id, read_status, rating, notes, review, is_favorite,
		                        read_status_updated_at, rating_updated_at, is_favorite_updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW(), NOW())
		ON CONFLICT (user_id, book_id) DO UPDATE SET
		    read_status = EXCLUDED.read_status,
		    rating      = EXCLUDED.rating,
		    notes       = EXCLUDED.notes,
		    review      = EXCLUDED.review,
		    is_favorite = EXCLUDED.is_favorite,
		    read_status_updated_at = CASE WHEN EXCLUDED.read_status IS DISTINCT FROM user_books.read_status
		                                 THEN NOW() ELSE user_books.read_status_updated_at END,
		    rating_updated_at      = CASE WHEN EXCLUDED.rating IS DISTINCT FROM user_books.rating
		                                 THEN NOW() ELSE user_books.rating_updated_at END,
		    is_favorite_updated_at = CASE WHEN EXCLUDED.is_favorite IS DISTINCT FROM user_books.is_favorite
		                                 THEN NOW() ELSE user_books.is_favorite_updated_at END,
		    updated_at  = NOW(),
		    deleted_at  = NULL`

	if _, err := r.db.Exec(ctx, q, userID, bookID, readStatus, rating, notes, review, isFavorite); err != nil {
		return nil, fmt.Errorf("writing reading state: %w", err)
	}
	if err := r.recordSession(ctx, userID, bookID, editionID, readStatus, dateStarted, dateFinished, progress); err != nil {
		return nil, err
	}
	return r.readInteraction(ctx, userID, editionID, bookID)
}

// MergeInteraction updates only the fields the caller supplied, leaving the
// rest as they were.
func (r *EditionRepo) MergeInteraction(ctx context.Context, userID, editionID uuid.UUID, readStatus *string, rating any, notes, review any, dateStarted, dateFinished any, isFavorite *bool, progress []byte) (*models.UserBookInteraction, error) {
	bookID, err := r.bookOfEdition(ctx, editionID)
	if err != nil {
		return nil, err
	}

	const q = `
		INSERT INTO user_books (user_id, book_id, read_status, rating, notes, review, is_favorite,
		                        read_status_updated_at, rating_updated_at, is_favorite_updated_at)
		VALUES ($1, $2, COALESCE(NULLIF($3,''), 'unread'), $4, COALESCE($5,''), COALESCE($6,''),
		        COALESCE($7, FALSE),
		        CASE WHEN NULLIF($3,'') IS NOT NULL THEN NOW() END,
		        CASE WHEN $4 IS NOT NULL THEN NOW() END,
		        CASE WHEN $7 IS NOT NULL THEN NOW() END)
		ON CONFLICT (user_id, book_id) DO UPDATE SET
		    read_status = COALESCE(NULLIF($3,''), user_books.read_status),
		    rating      = COALESCE($4, user_books.rating),
		    notes       = COALESCE($5, user_books.notes),
		    review      = COALESCE($6, user_books.review),
		    is_favorite = COALESCE($7, user_books.is_favorite),
		    read_status_updated_at = CASE WHEN NULLIF($3,'') IS DISTINCT FROM NULL
		                                  AND NULLIF($3,'') IS DISTINCT FROM user_books.read_status
		                                 THEN NOW() ELSE user_books.read_status_updated_at END,
		    rating_updated_at      = CASE WHEN $4 IS NOT NULL AND $4 IS DISTINCT FROM user_books.rating
		                                 THEN NOW() ELSE user_books.rating_updated_at END,
		    is_favorite_updated_at = CASE WHEN $7 IS NOT NULL AND $7 IS DISTINCT FROM user_books.is_favorite
		                                 THEN NOW() ELSE user_books.is_favorite_updated_at END,
		    updated_at  = NOW(),
		    deleted_at  = NULL`

	if _, err := r.db.Exec(ctx, q, userID, bookID, readStatus, rating, notes, review, isFavorite); err != nil {
		return nil, fmt.Errorf("merging reading state: %w", err)
	}
	status := ""
	if readStatus != nil {
		status = *readStatus
	}
	if err := r.recordSession(ctx, userID, bookID, editionID, status, dateStarted, dateFinished, progress); err != nil {
		return nil, err
	}
	return r.readInteraction(ctx, userID, editionID, bookID)
}

// recordSession keeps one open session per work in step with a caller that only
// knows about dates and progress.
//
// It updates the most recent session rather than always adding one, because the
// old API has no notion of a session: a client setting a finish date twice means
// one reading that got corrected, not two readings. Deliberately logging a
// reread is what the sessions endpoint is for.
func (r *EditionRepo) recordSession(ctx context.Context, userID, bookID, editionID uuid.UUID, readStatus string, dateStarted, dateFinished any, progress []byte) error {
	if dateStarted == nil && dateFinished == nil && len(progress) == 0 {
		return nil
	}

	status := "reading"
	switch readStatus {
	case "read":
		status = "finished"
	case "did_not_finish":
		status = "abandoned"
	}

	const q = `
		WITH latest AS (
		    SELECT id FROM reading_sessions
		     WHERE user_id = $1 AND book_id = $2
		     ORDER BY COALESCE(started_at, created_at) DESC, created_at DESC
		     LIMIT 1
		), updated AS (
		    UPDATE reading_sessions rs
		       SET started_at     = COALESCE($4::timestamptz, rs.started_at),
		           finished_at    = COALESCE($5::timestamptz, rs.finished_at),
		           status         = $6,
		           progress_unit  = COALESCE($7, rs.progress_unit),
		           progress_value = COALESCE($8, rs.progress_value)
		      FROM latest WHERE rs.id = latest.id
		    RETURNING rs.id
		)
		INSERT INTO reading_sessions (user_id, book_id, edition_id, started_at, finished_at,
		                              status, progress_unit, progress_value)
		SELECT $1, $2, $3, $4::timestamptz, $5::timestamptz, $6, $7, $8
		 WHERE NOT EXISTS (SELECT 1 FROM updated)`

	var unit, value any
	if len(progress) > 0 {
		// The old column was an unvalidated blob. Only a percentage survives
		// the move, because that is the one shape that means the same thing
		// without knowing which printing it counts.
		var parsed struct {
			Percent *float64 `json:"percent"`
		}
		if err := json.Unmarshal(progress, &parsed); err == nil && parsed.Percent != nil {
			unit, value = "percent", *parsed.Percent
		}
	}

	if _, err := r.db.Exec(ctx, q, userID, bookID, editionID, dateStarted, dateFinished, status, unit, value); err != nil {
		return fmt.Errorf("recording reading session: %w", err)
	}
	return nil
}

// DeleteInteraction forgets the caller's reading state for the work.
func (r *EditionRepo) DeleteInteraction(ctx context.Context, userID, editionID uuid.UUID) error {
	bookID, err := r.bookOfEdition(ctx, editionID)
	if err != nil {
		return err
	}
	result, err := r.db.Exec(ctx,
		`UPDATE user_books SET deleted_at = NOW(), updated_at = NOW()
		  WHERE user_id = $1 AND book_id = $2 AND deleted_at IS NULL`,
		userID, bookID)
	if err != nil {
		return fmt.Errorf("deleting reading state: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// precisionOrDay fills in a precision for callers that have a date but never
// established how specific it was. Day is the honest default for a value that
// arrived already parsed: it claims exactly what the stored date says.
func precisionOrDay(p models.DatePrecision) models.DatePrecision {
	if p == "" {
		return models.DatePrecisionDay
	}
	return p
}
