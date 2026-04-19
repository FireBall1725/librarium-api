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

type TagRepo struct {
	db *pgxpool.Pool
}

func NewTagRepo(db *pgxpool.Pool) *TagRepo {
	return &TagRepo{db: db}
}

func (r *TagRepo) List(ctx context.Context, libraryID uuid.UUID) ([]*models.Tag, error) {
	const q = `
		SELECT id, library_id, name, COALESCE(color,''), created_at
		FROM tags
		WHERE library_id = $1
		ORDER BY name`
	rows, err := r.db.Query(ctx, q, libraryID)
	if err != nil {
		return nil, fmt.Errorf("listing tags: %w", err)
	}
	defer rows.Close()

	var out []*models.Tag
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *TagRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Tag, error) {
	const q = `SELECT id, library_id, name, COALESCE(color,''), created_at FROM tags WHERE id = $1`
	t, err := scanTag(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding tag: %w", err)
	}
	return t, nil
}

func (r *TagRepo) Create(ctx context.Context, id, libraryID uuid.UUID, name, color string, createdBy uuid.UUID) (*models.Tag, error) {
	const q = `
		INSERT INTO tags (id, library_id, name, color, created_by)
		VALUES ($1, $2, $3, NULLIF($4,''), $5)
		RETURNING id, library_id, name, COALESCE(color,''), created_at`
	t, err := scanTag(r.db.QueryRow(ctx, q, id, libraryID, name, color, createdBy))
	if err != nil {
		return nil, fmt.Errorf("inserting tag: %w", err)
	}
	return t, nil
}

func (r *TagRepo) Update(ctx context.Context, id uuid.UUID, name, color string) (*models.Tag, error) {
	const q = `
		UPDATE tags SET name = $2, color = NULLIF($3,'')
		WHERE id = $1
		RETURNING id, library_id, name, COALESCE(color,''), created_at`
	t, err := scanTag(r.db.QueryRow(ctx, q, id, name, color))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("updating tag: %w", err)
	}
	return t, nil
}

func (r *TagRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `DELETE FROM tags WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting tag: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Junction-table setters ───────────────────────────────────────────────────

// SetBookTags replaces all tags on a book within the given transaction.
func (r *TagRepo) SetBookTags(ctx context.Context, tx pgx.Tx, bookID uuid.UUID, tagIDs []uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM book_tags WHERE book_id = $1`, bookID); err != nil {
		return fmt.Errorf("clearing book tags: %w", err)
	}
	for _, tid := range tagIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO book_tags (book_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, bookID, tid); err != nil {
			return fmt.Errorf("inserting book tag: %w", err)
		}
	}
	return nil
}

func (r *TagRepo) SetSeriesTags(ctx context.Context, seriesID uuid.UUID, tagIDs []uuid.UUID) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM series_tags WHERE series_id = $1`, seriesID); err != nil {
		return fmt.Errorf("clearing series tags: %w", err)
	}
	for _, tid := range tagIDs {
		if _, err := r.db.Exec(ctx, `INSERT INTO series_tags (series_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, seriesID, tid); err != nil {
			return fmt.Errorf("inserting series tag: %w", err)
		}
	}
	return nil
}

func (r *TagRepo) SetShelfTags(ctx context.Context, shelfID uuid.UUID, tagIDs []uuid.UUID) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM shelf_tags WHERE shelf_id = $1`, shelfID); err != nil {
		return fmt.Errorf("clearing shelf tags: %w", err)
	}
	for _, tid := range tagIDs {
		if _, err := r.db.Exec(ctx, `INSERT INTO shelf_tags (shelf_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, shelfID, tid); err != nil {
			return fmt.Errorf("inserting shelf tag: %w", err)
		}
	}
	return nil
}

func (r *TagRepo) SetLoanTags(ctx context.Context, loanID uuid.UUID, tagIDs []uuid.UUID) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM loan_tags WHERE loan_id = $1`, loanID); err != nil {
		return fmt.Errorf("clearing loan tags: %w", err)
	}
	for _, tid := range tagIDs {
		if _, err := r.db.Exec(ctx, `INSERT INTO loan_tags (loan_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, loanID, tid); err != nil {
			return fmt.Errorf("inserting loan tag: %w", err)
		}
	}
	return nil
}

func (r *TagRepo) SetMemberTags(ctx context.Context, libraryID, userID uuid.UUID, tagIDs []uuid.UUID) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM member_tags WHERE library_id = $1 AND user_id = $2`, libraryID, userID); err != nil {
		return fmt.Errorf("clearing member tags: %w", err)
	}
	for _, tid := range tagIDs {
		if _, err := r.db.Exec(ctx, `INSERT INTO member_tags (library_id, user_id, tag_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, libraryID, userID, tid); err != nil {
			return fmt.Errorf("inserting member tag: %w", err)
		}
	}
	return nil
}

func scanTag(s scanner) (*models.Tag, error) {
	var (
		pgID        pgtype.UUID
		pgLibraryID pgtype.UUID
		t           models.Tag
	)
	if err := s.Scan(&pgID, &pgLibraryID, &t.Name, &t.Color, &t.CreatedAt); err != nil {
		return nil, err
	}
	t.ID = uuid.UUID(pgID.Bytes)
	t.LibraryID = uuid.UUID(pgLibraryID.Bytes)
	return &t, nil
}
