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

type ShelfRepo struct {
	db *pgxpool.Pool
}

func NewShelfRepo(db *pgxpool.Pool) *ShelfRepo {
	return &ShelfRepo{db: db}
}

const shelfTagsSubquery = `
    COALESCE(
        (SELECT json_agg(json_build_object('id', t.id, 'name', t.name, 'color', t.color) ORDER BY t.name)
         FROM shelf_tags st JOIN tags t ON t.id = st.tag_id WHERE st.shelf_id = s.id),
        '[]'::json
    )`

func (r *ShelfRepo) List(ctx context.Context, libraryID uuid.UUID, search, tagFilter string) ([]*models.Shelf, error) {
	args := []any{libraryID}
	where := `WHERE s.library_id = $1`
	if search != "" {
		args = append(args, "%"+search+"%")
		where += fmt.Sprintf(` AND lower(s.name) LIKE lower($%d)`, len(args))
	}
	if tagFilter != "" {
		args = append(args, tagFilter)
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM shelf_tags st JOIN tags t ON t.id = st.tag_id WHERE st.shelf_id = s.id AND lower(t.name) = lower($%d))`, len(args))
	}

	q := `
		SELECT s.id, s.library_id, s.name, COALESCE(s.description,''),
		       COALESCE(s.color,''), COALESCE(s.icon,''), s.display_order,
		       COUNT(bs.book_id) AS book_count,
		       s.created_at, s.updated_at,
		       ` + shelfTagsSubquery + ` AS tags
		FROM shelves s
		LEFT JOIN book_shelves bs ON bs.shelf_id = s.id
		` + where + `
		GROUP BY s.id
		ORDER BY s.display_order, s.name`

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing shelves: %w", err)
	}
	defer rows.Close()

	var out []*models.Shelf
	for rows.Next() {
		s, err := scanShelf(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *ShelfRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Shelf, error) {
	q := `
		SELECT s.id, s.library_id, s.name, COALESCE(s.description,''),
		       COALESCE(s.color,''), COALESCE(s.icon,''), s.display_order,
		       COUNT(bs.book_id) AS book_count,
		       s.created_at, s.updated_at,
		       ` + shelfTagsSubquery + ` AS tags
		FROM shelves s
		LEFT JOIN book_shelves bs ON bs.shelf_id = s.id
		WHERE s.id = $1
		GROUP BY s.id`
	s, err := scanShelf(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding shelf: %w", err)
	}
	return s, nil
}

func (r *ShelfRepo) Create(ctx context.Context, id, libraryID uuid.UUID, name, description, color, icon string, displayOrder int, createdBy uuid.UUID) (*models.Shelf, error) {
	const q = `
		INSERT INTO shelves (id, library_id, name, description, color, icon, display_order, created_by)
		VALUES ($1, $2, $3, NULLIF($4,''), NULLIF($5,''), NULLIF($6,''), $7, $8)`
	if _, err := r.db.Exec(ctx, q, id, libraryID, name, description, color, icon, displayOrder, createdBy); err != nil {
		return nil, fmt.Errorf("inserting shelf: %w", err)
	}
	return r.FindByID(ctx, id)
}

func (r *ShelfRepo) Update(ctx context.Context, id uuid.UUID, name, description, color, icon string, displayOrder int) (*models.Shelf, error) {
	const q = `
		UPDATE shelves
		SET name          = $2,
		    description   = NULLIF($3,''),
		    color         = NULLIF($4,''),
		    icon          = NULLIF($5,''),
		    display_order = $6
		WHERE id = $1`
	result, err := r.db.Exec(ctx, q, id, name, description, color, icon, displayOrder)
	if err != nil {
		return nil, fmt.Errorf("updating shelf: %w", err)
	}
	if result.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.FindByID(ctx, id)
}

func (r *ShelfRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `DELETE FROM shelves WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting shelf: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Book → shelves ───────────────────────────────────────────────────────────

// FindByBook returns all shelves (in this library) that contain the given book.
func (r *ShelfRepo) FindByBook(ctx context.Context, libraryID, bookID uuid.UUID) ([]*models.Shelf, error) {
	q := `
		SELECT s.id, s.library_id, s.name, COALESCE(s.description,''),
		       COALESCE(s.color,''), COALESCE(s.icon,''), s.display_order,
		       (SELECT COUNT(*) FROM book_shelves bs2 WHERE bs2.shelf_id = s.id) AS book_count,
		       s.created_at, s.updated_at,
		       ` + shelfTagsSubquery + ` AS tags
		FROM shelves s
		JOIN book_shelves bs ON bs.shelf_id = s.id AND bs.book_id = $2
		WHERE s.library_id = $1
		ORDER BY s.display_order, s.name`
	rows, err := r.db.Query(ctx, q, libraryID, bookID)
	if err != nil {
		return nil, fmt.Errorf("finding book shelves: %w", err)
	}
	defer rows.Close()

	var out []*models.Shelf
	for rows.Next() {
		s, err := scanShelf(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ─── Shelf books ──────────────────────────────────────────────────────────────

func (r *ShelfRepo) ListBooks(ctx context.Context, shelfID uuid.UUID) ([]*models.Book, error) {
	q := booksSelect(0, 0) + `
		JOIN book_shelves bs ON bs.book_id = b.id
		WHERE bs.shelf_id = $1
		ORDER BY bs.added_at DESC`
	rows, err := r.db.Query(ctx, q, shelfID)
	if err != nil {
		return nil, fmt.Errorf("listing shelf books: %w", err)
	}
	defer rows.Close()

	var out []*models.Book
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *ShelfRepo) AddBook(ctx context.Context, shelfID, bookID, addedBy uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO book_shelves (book_id, shelf_id, added_by) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		bookID, shelfID, addedBy,
	)
	if err != nil {
		return fmt.Errorf("adding book to shelf: %w", err)
	}
	return nil
}

func (r *ShelfRepo) RemoveBook(ctx context.Context, shelfID, bookID uuid.UUID) error {
	result, err := r.db.Exec(ctx,
		`DELETE FROM book_shelves WHERE shelf_id = $1 AND book_id = $2`,
		shelfID, bookID,
	)
	if err != nil {
		return fmt.Errorf("removing book from shelf: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanShelf(s scanner) (*models.Shelf, error) {
	var (
		pgID        pgtype.UUID
		pgLibraryID pgtype.UUID
		tagsJSON    []byte
		sh          models.Shelf
	)
	err := s.Scan(
		&pgID, &pgLibraryID, &sh.Name, &sh.Description,
		&sh.Color, &sh.Icon, &sh.DisplayOrder,
		&sh.BookCount, &sh.CreatedAt, &sh.UpdatedAt,
		&tagsJSON,
	)
	if err != nil {
		return nil, err
	}
	sh.ID = uuid.UUID(pgID.Bytes)
	sh.LibraryID = uuid.UUID(pgLibraryID.Bytes)
	if err := json.Unmarshal(tagsJSON, &sh.Tags); err != nil || sh.Tags == nil {
		sh.Tags = []*models.Tag{}
	}
	return &sh, nil
}
