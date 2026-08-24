// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrEditionNotOfBook is returned when a copy would claim a printing of a
// different work.
//
// The database refuses this through a composite foreign key, because a plain
// key on edition_id alone lets a copy claim Dune the work with a Neuromancer
// edition and Postgres accepts it silently.
var ErrEditionNotOfBook = errors.New("that edition does not belong to that book")

// ErrLocationNotInLibrary is returned when a copy would be filed at a shelf
// belonging to another library. Also enforced by a composite foreign key.
var ErrLocationNotInLibrary = errors.New("that location belongs to a different library")

type CopyRepo struct {
	db *pgxpool.Pool
}

func NewCopyRepo(db *pgxpool.Pool) *CopyRepo {
	return &CopyRepo{db: db}
}

// copyColumns is shared by every read so the scan sites cannot drift apart.
// Changing a SELECT in this file means checking this one place rather than
// three, which is the trap the older repositories set.
const copyColumns = `
	c.id, c.library_id, c.book_id, c.edition_id,
	c.acquired_at, c.acquired_from, c.acquired_by,
	c.price_minor, COALESCE(c.price_currency, ''),
	COALESCE(c.condition, ''), c.is_signed, c.notes,
	c.location_id, COALESCE(l.name, ''),
	COALESCE(ln.loaned_to, ''),
	c.created_at, c.updated_at, c.deleted_at`

const copyFrom = `
	FROM copies c
	LEFT JOIN copy_locations l ON l.id = c.location_id
	LEFT JOIN loans ln ON ln.copy_id = c.id AND ln.returned_at IS NULL AND ln.deleted_at IS NULL`

func scanCopy(row pgx.Row) (*models.Copy, error) {
	var c models.Copy
	err := row.Scan(
		&c.ID, &c.LibraryID, &c.BookID, &c.EditionID,
		&c.AcquiredAt, &c.AcquiredFrom, &c.AcquiredBy,
		&c.PriceMinor, &c.PriceCurrency,
		&c.Condition, &c.IsSigned, &c.Notes,
		&c.LocationID, &c.LocationName,
		&c.OnLoanTo,
		&c.CreatedAt, &c.UpdatedAt, &c.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *CopyRepo) collect(ctx context.Context, q string, args ...any) ([]*models.Copy, error) {
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing copies: %w", err)
	}
	defer rows.Close()

	out := make([]*models.Copy, 0)
	for rows.Next() {
		c, err := scanCopy(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning copy: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListForBook returns every copy of a work across the libraries given.
//
// The caller passes the libraries it has already decided the reader may see;
// this does not resolve permissions itself.
func (r *CopyRepo) ListForBook(ctx context.Context, bookID uuid.UUID, libraryIDs []uuid.UUID) ([]*models.Copy, error) {
	if len(libraryIDs) == 0 {
		return make([]*models.Copy, 0), nil
	}
	q := `SELECT ` + copyColumns + copyFrom + `
		 WHERE c.book_id = $1 AND c.library_id = ANY($2) AND c.deleted_at IS NULL
		 ORDER BY c.is_signed DESC, c.created_at, c.id`
	return r.collect(ctx, q, bookID, libraryIDs)
}

// ListForLibrary returns a library's copies, newest acquisitions first.
func (r *CopyRepo) ListForLibrary(ctx context.Context, libraryID uuid.UUID, limit, offset int) ([]*models.Copy, error) {
	q := `SELECT ` + copyColumns + copyFrom + `
		 WHERE c.library_id = $1 AND c.deleted_at IS NULL
		 ORDER BY c.acquired_at DESC NULLS LAST, c.created_at DESC, c.id
		 LIMIT $2 OFFSET $3`
	return r.collect(ctx, q, libraryID, limit, offset)
}

func (r *CopyRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Copy, error) {
	q := `SELECT ` + copyColumns + copyFrom + ` WHERE c.id = $1 AND c.deleted_at IS NULL`
	c, err := scanCopy(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding copy: %w", err)
	}
	return c, nil
}

// CountForBook reports how many copies of a work exist in the given libraries.
// Used where the interface only needs "do I have this, and how many".
func (r *CopyRepo) CountForBook(ctx context.Context, bookID uuid.UUID, libraryIDs []uuid.UUID) (int, error) {
	if len(libraryIDs) == 0 {
		return 0, nil
	}
	const q = `
		SELECT count(*) FROM copies
		 WHERE book_id = $1 AND library_id = ANY($2) AND deleted_at IS NULL`

	var n int
	if err := r.db.QueryRow(ctx, q, bookID, libraryIDs).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting copies: %w", err)
	}
	return n, nil
}

// CreateCopyInput is what a caller supplies to record an object. Everything
// optional is optional on purpose: adding a book creates one copy with every
// field empty, so nobody meets the word "copy" until they own two.
type CreateCopyInput struct {
	LibraryID     uuid.UUID
	BookID        uuid.UUID
	EditionID     *uuid.UUID
	AcquiredAt    *string
	AcquiredFrom  string
	AcquiredBy    *uuid.UUID
	PriceMinor    *int64
	PriceCurrency string
	Condition     string
	IsSigned      bool
	Notes         string
	LocationID    *uuid.UUID
}

func (r *CopyRepo) Create(ctx context.Context, in CreateCopyInput) (*models.Copy, error) {
	const q = `
		INSERT INTO copies (library_id, book_id, edition_id, acquired_at, acquired_from,
		                    acquired_by, price_minor, price_currency, condition,
		                    is_signed, notes, location_id)
		VALUES ($1, $2, $3, $4::date, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id`

	var id uuid.UUID
	err := r.db.QueryRow(ctx, q,
		in.LibraryID, in.BookID, in.EditionID, in.AcquiredAt, in.AcquiredFrom,
		in.AcquiredBy, in.PriceMinor, nullIfEmpty(in.PriceCurrency), nullIfEmpty(in.Condition),
		in.IsSigned, in.Notes, in.LocationID,
	).Scan(&id)
	if err != nil {
		return nil, translateCopyErr(err)
	}
	return r.FindByID(ctx, id)
}

// UpdateCopyInput carries only what changed. A nil pointer leaves the column
// alone, which is why every field is one.
type UpdateCopyInput struct {
	EditionID     **uuid.UUID
	AcquiredAt    **string
	AcquiredFrom  *string
	PriceMinor    **int64
	PriceCurrency *string
	Condition     *string
	IsSigned      *bool
	Notes         *string
	LocationID    **uuid.UUID
}

func (r *CopyRepo) Update(ctx context.Context, id uuid.UUID, in UpdateCopyInput) (*models.Copy, error) {
	sets := make([]string, 0, 9)
	args := make([]any, 0, 10)
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}

	if in.EditionID != nil {
		add("edition_id", *in.EditionID)
	}
	if in.AcquiredAt != nil {
		args = append(args, *in.AcquiredAt)
		sets = append(sets, fmt.Sprintf("acquired_at = $%d::date", len(args)))
	}
	if in.AcquiredFrom != nil {
		add("acquired_from", *in.AcquiredFrom)
	}
	if in.PriceMinor != nil {
		add("price_minor", *in.PriceMinor)
	}
	if in.PriceCurrency != nil {
		add("price_currency", nullIfEmpty(*in.PriceCurrency))
	}
	if in.Condition != nil {
		add("condition", nullIfEmpty(*in.Condition))
	}
	if in.IsSigned != nil {
		add("is_signed", *in.IsSigned)
	}
	if in.Notes != nil {
		add("notes", *in.Notes)
	}
	if in.LocationID != nil {
		add("location_id", *in.LocationID)
	}

	if len(sets) == 0 {
		return r.FindByID(ctx, id)
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, id)

	q := fmt.Sprintf(`UPDATE copies SET %s WHERE id = $%d AND deleted_at IS NULL`,
		strings.Join(sets, ", "), len(args))

	tag, err := r.db.Exec(ctx, q, args...)
	if err != nil {
		return nil, translateCopyErr(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.FindByID(ctx, id)
}

// Delete soft-deletes a copy. Losing an object by accident is the expensive
// case, so this is recoverable; the row stays until something prunes it.
func (r *CopyRepo) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `UPDATE copies SET deleted_at = NOW(), updated_at = NOW()
	            WHERE id = $1 AND deleted_at IS NULL`

	tag, err := r.db.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("deleting copy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// translateCopyErr turns the two composite foreign keys into errors a handler
// can map to a useful message. Both are constraints that only exist because a
// plain foreign key would have let the bad state through silently.
func translateCopyErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		switch {
		case strings.Contains(pgErr.ConstraintName, "edition_matches_book"):
			return ErrEditionNotOfBook
		case strings.Contains(pgErr.ConstraintName, "location_same_library"):
			return ErrLocationNotInLibrary
		default:
			return ErrNotFound
		}
	}
	return fmt.Errorf("writing copy: %w", err)
}

// nullIfEmpty keeps an empty string out of a column whose absence means
// "unknown". condition and price_currency are foreign keys or checked, so ”
// would be rejected where NULL is the correct way to say nothing.
func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
