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
		       (SELECT count(*) FROM library_shelf_books bs WHERE bs.shelf_id = s.id) AS book_count,
		       s.created_at, s.updated_at,
		       ` + shelfTagsSubquery + ` AS tags
		FROM library_shelves s
		` + where + `
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

// ListAcross returns every shelf in the given libraries, for the rail.
//
// The rail lists shelves the way it lists libraries and views, so it needs them
// across the caller's whole scope rather than one library at a time. Ordered by
// library first so the rows group by the library they belong to without the
// rail having to sort them itself.
func (r *ShelfRepo) ListAcross(ctx context.Context, libraryIDs []uuid.UUID) ([]*models.Shelf, error) {
	if len(libraryIDs) == 0 {
		return []*models.Shelf{}, nil
	}
	q := `
		SELECT s.id, s.library_id, s.name, COALESCE(s.description,''),
		       COALESCE(s.color,''), COALESCE(s.icon,''), s.display_order,
		       (SELECT count(*) FROM library_shelf_books bs WHERE bs.shelf_id = s.id) AS book_count,
		       s.created_at, s.updated_at,
		       ` + shelfTagsSubquery + ` AS tags
		FROM library_shelves s
		JOIN libraries l ON l.id = s.library_id
		WHERE s.library_id = ANY($1)
		ORDER BY lower(l.name), s.display_order, s.name`

	rows, err := r.db.Query(ctx, q, libraryIDs)
	if err != nil {
		return nil, fmt.Errorf("listing shelves across libraries: %w", err)
	}
	defer rows.Close()

	out := make([]*models.Shelf, 0)
	for rows.Next() {
		sh, err := scanShelf(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

func (r *ShelfRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Shelf, error) {
	q := `
		SELECT s.id, s.library_id, s.name, COALESCE(s.description,''),
		       COALESCE(s.color,''), COALESCE(s.icon,''), s.display_order,
		       (SELECT count(*) FROM library_shelf_books bs WHERE bs.shelf_id = s.id) AS book_count,
		       s.created_at, s.updated_at,
		       ` + shelfTagsSubquery + ` AS tags
		FROM library_shelves s
		WHERE s.id = $1`
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
	// A shelf is a manual list shared with a library. Writing lists directly
	// rather than through library_shelves, which is a view and not writable, and
	// which would hide the kind and visibility that make it shelf-shaped at all.
	const q = `
		INSERT INTO lists (id, owner_user_id, shared_library_id, name, description,
		                   color, icon, display_order, kind, visibility)
		VALUES ($1, $8, $2, $3, COALESCE($4,''), COALESCE($5,''), COALESCE($6,''), $7,
		        'manual', 'library')`
	if _, err := r.db.Exec(ctx, q, id, libraryID, name, description, color, icon, displayOrder, createdBy); err != nil {
		return nil, fmt.Errorf("inserting shelf: %w", err)
	}
	return r.FindByID(ctx, id)
}

func (r *ShelfRepo) Update(ctx context.Context, id uuid.UUID, name, description, color, icon string, displayOrder int) (*models.Shelf, error) {
	const q = `
		UPDATE lists
		SET name          = $2,
		    description   = COALESCE($3,''),
		    color         = COALESCE($4,''),
		    icon          = COALESCE($5,''),
		    display_order = $6,
		    updated_at    = NOW()
		WHERE id = $1 AND kind = 'manual' AND visibility = 'library'`
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
	result, err := r.db.Exec(ctx, `DELETE FROM lists WHERE id = $1 AND kind = 'manual' AND visibility = 'library'`, id)
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
		       (SELECT COUNT(*) FROM library_shelf_books bs2 WHERE bs2.shelf_id = s.id) AS book_count,
		       s.created_at, s.updated_at,
		       ` + shelfTagsSubquery + ` AS tags
		FROM library_shelves s
		JOIN library_shelf_books bs ON bs.shelf_id = s.id AND bs.book_id = $2
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
	q := booksSelect(0, 0, false, false) + `
		JOIN library_shelf_books bs ON bs.book_id = b.id
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

// AddBook puts a book on a shelf.
//
// addedBy is accepted and unused: list_books records when a book joined a list
// but not who put it there, because a list belongs to one person and that
// question has one answer. The parameter stays so the shelf routes keep their
// signature.
func (r *ShelfRepo) AddBook(ctx context.Context, shelfID, bookID, addedBy uuid.UUID) error {
	_ = addedBy
	_, err := r.db.Exec(ctx,
		`INSERT INTO list_books (book_id, list_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		bookID, shelfID,
	)
	if err != nil {
		return fmt.Errorf("adding book to shelf: %w", err)
	}
	return nil
}

// AddBookTx puts a book on a shelf inside a caller's transaction, so an import
// that fails partway leaves no shelf membership pointing at a book that was
// rolled back. Membership is additive: a book already on the shelf stays put.
func (r *ShelfRepo) AddBookTx(ctx context.Context, tx pgx.Tx, shelfID, bookID uuid.UUID) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO list_books (book_id, list_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		bookID, shelfID,
	)
	if err != nil {
		return fmt.Errorf("adding book to shelf: %w", err)
	}
	return nil
}

func (r *ShelfRepo) RemoveBook(ctx context.Context, shelfID, bookID uuid.UUID) error {
	result, err := r.db.Exec(ctx,
		`DELETE FROM list_books WHERE list_id = $1 AND book_id = $2`,
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
