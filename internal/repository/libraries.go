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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LibraryRepo struct {
	db *pgxpool.Pool
}

func NewLibraryRepo(db *pgxpool.Pool) *LibraryRepo {
	return &LibraryRepo{db: db}
}

const libraryColumns = `id, name, description, slug, owner_id, is_public, created_at, updated_at`

// libraryListColumns extends libraryColumns with caller-scoped count
// columns for the list endpoints. callerArg is the positional arg index
// holding the calling user id; pass 0 to skip the per-user counts (they
// will return 0).
func libraryListColumns(callerArg int) string {
	if callerArg <= 0 {
		return libraryColumns + `,
		COALESCE((SELECT COUNT(*) FROM library_books lb WHERE lb.library_id = l.id), 0) AS book_count,
		0 AS reading_count,
		0 AS read_count`
	}
	return libraryColumns + `,
		COALESCE((SELECT COUNT(*) FROM library_books lb WHERE lb.library_id = l.id), 0) AS book_count,
		COALESCE((
			SELECT COUNT(DISTINCT lb.book_id)
			FROM library_books lb
			JOIN book_editions be ON be.book_id = lb.book_id
			JOIN user_book_interactions ubi ON ubi.book_edition_id = be.id
			WHERE lb.library_id = l.id AND ubi.user_id = $` + fmt.Sprint(callerArg) + ` AND ubi.read_status = 'reading'
		), 0) AS reading_count,
		COALESCE((
			SELECT COUNT(DISTINCT lb.book_id)
			FROM library_books lb
			JOIN book_editions be ON be.book_id = lb.book_id
			JOIN user_book_interactions ubi ON ubi.book_edition_id = be.id
			WHERE lb.library_id = l.id AND ubi.user_id = $` + fmt.Sprint(callerArg) + ` AND ubi.read_status = 'read'
		), 0) AS read_count`
}

func scanLibrary(s scanner) (*models.Library, error) {
	var (
		pgID      pgtype.UUID
		pgOwnerID pgtype.UUID
		pgDesc    pgtype.Text
		lib       models.Library
	)
	err := s.Scan(&pgID, &lib.Name, &pgDesc, &lib.Slug, &pgOwnerID, &lib.IsPublic, &lib.CreatedAt, &lib.UpdatedAt)
	if err != nil {
		return nil, err
	}
	lib.ID = uuid.UUID(pgID.Bytes)
	lib.OwnerID = uuid.UUID(pgOwnerID.Bytes)
	lib.Description = pgDesc.String // empty string when NULL
	return &lib, nil
}

// scanLibraryWithCounts is the list-path scanner — same as scanLibrary
// plus the three count columns added by libraryListColumns.
func scanLibraryWithCounts(s scanner) (*models.Library, error) {
	var (
		pgID      pgtype.UUID
		pgOwnerID pgtype.UUID
		pgDesc    pgtype.Text
		lib       models.Library
	)
	err := s.Scan(
		&pgID, &lib.Name, &pgDesc, &lib.Slug, &pgOwnerID, &lib.IsPublic, &lib.CreatedAt, &lib.UpdatedAt,
		&lib.BookCount, &lib.ReadingCount, &lib.ReadCount,
	)
	if err != nil {
		return nil, err
	}
	lib.ID = uuid.UUID(pgID.Bytes)
	lib.OwnerID = uuid.UUID(pgOwnerID.Bytes)
	lib.Description = pgDesc.String
	return &lib, nil
}

func (r *LibraryRepo) Create(ctx context.Context, tx pgx.Tx, id uuid.UUID, name, description, slug string, ownerID uuid.UUID, isPublic bool) (*models.Library, error) {
	const q = `
		INSERT INTO libraries (id, name, description, slug, owner_id, is_public)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6)
		RETURNING ` + libraryColumns

	lib, err := scanLibrary(tx.QueryRow(ctx, q, id, name, description, slug, ownerID, isPublic))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("inserting library: %w", err)
	}
	return lib, nil
}

func (r *LibraryRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Library, error) {
	q := `SELECT ` + libraryColumns + ` FROM libraries WHERE id = $1`
	lib, err := scanLibrary(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding library: %w", err)
	}
	return lib, nil
}

func (r *LibraryRepo) ExistsBySlug(ctx context.Context, slug string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM libraries WHERE slug = $1)`, slug).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking slug: %w", err)
	}
	return exists, nil
}

// ListForUser returns all libraries in which the user holds a membership,
// each populated with caller-scoped counts (book_count global, reading +
// read counts for the same userID).
func (r *LibraryRepo) ListForUser(ctx context.Context, userID uuid.UUID) ([]*models.Library, error) {
	q := `
		SELECT l.` + libraryListColumns(1) + `
		FROM libraries l
		JOIN library_memberships lm ON lm.library_id = l.id
		WHERE lm.user_id = $1
		ORDER BY l.name`

	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("listing libraries for user: %w", err)
	}
	defer rows.Close()
	return collectLibrariesWithCounts(rows)
}

// ListAll returns every library. Used by instance admins. callerID is
// optional — when non-zero, reading + read counts are scoped to that
// user; otherwise they're zero (book_count is always populated).
func (r *LibraryRepo) ListAll(ctx context.Context, callerID uuid.UUID) ([]*models.Library, error) {
	if callerID == uuid.Nil {
		q := `SELECT ` + libraryListColumns(0) + ` FROM libraries l ORDER BY l.name`
		rows, err := r.db.Query(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("listing all libraries: %w", err)
		}
		defer rows.Close()
		return collectLibrariesWithCounts(rows)
	}
	q := `SELECT ` + libraryListColumns(1) + ` FROM libraries l ORDER BY l.name`
	rows, err := r.db.Query(ctx, q, callerID)
	if err != nil {
		return nil, fmt.Errorf("listing all libraries: %w", err)
	}
	defer rows.Close()
	return collectLibrariesWithCounts(rows)
}

func (r *LibraryRepo) Update(ctx context.Context, id uuid.UUID, name, description string, isPublic bool) (*models.Library, error) {
	q := `
		UPDATE libraries
		SET name = $2, description = NULLIF($3, ''), is_public = $4
		WHERE id = $1
		RETURNING ` + libraryColumns

	lib, err := scanLibrary(r.db.QueryRow(ctx, q, id, name, description, isPublic))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("updating library: %w", err)
	}
	return lib, nil
}

func (r *LibraryRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `DELETE FROM libraries WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting library: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func collectLibrariesWithCounts(rows pgx.Rows) ([]*models.Library, error) {
	var out []*models.Library
	for rows.Next() {
		lib, err := scanLibraryWithCounts(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning library: %w", err)
		}
		out = append(out, lib)
	}
	return out, rows.Err()
}
