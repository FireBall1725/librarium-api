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

// ErrAlreadyWanted is returned when a catalogued book is already on this
// person's wishlist. Wanting something twice is not a state worth having.
var ErrAlreadyWanted = errors.New("that book is already on the wishlist")

type WishlistRepo struct {
	db *pgxpool.Pool
}

func NewWishlistRepo(db *pgxpool.Pool) *WishlistRepo {
	return &WishlistRepo{db: db}
}

const wishlistColumns = `
	w.id, w.user_id, w.book_id,
	COALESCE(w.title, COALESCE(b.title, '')),
	COALESCE(w.author_name, ''), w.notes, w.priority, w.created_at`

// List returns what someone wants, most wanted first.
//
// One query, not a union. The old shape split this by whether the thing was in
// the catalogue, so answering "what do I want" meant asking twice and merging.
func (r *WishlistRepo) List(ctx context.Context, userID uuid.UUID) ([]*models.WishlistEntry, error) {
	q := `SELECT ` + wishlistColumns + `
	        FROM wishlist w
	        LEFT JOIN books b ON b.id = w.book_id
	       WHERE w.user_id = $1
	       ORDER BY w.priority DESC, w.created_at DESC`

	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("listing wishlist: %w", err)
	}
	defer rows.Close()

	out := make([]*models.WishlistEntry, 0)
	for rows.Next() {
		var e models.WishlistEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.BookID, &e.Title,
			&e.AuthorName, &e.Notes, &e.Priority, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning wishlist entry: %w", err)
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// AddCatalogued records a want for a book the catalogue already knows.
func (r *WishlistRepo) AddCatalogued(ctx context.Context, userID, bookID uuid.UUID, notes string, priority int) (*models.WishlistEntry, error) {
	const q = `
		INSERT INTO wishlist (user_id, book_id, notes, priority)
		VALUES ($1, $2, $3, $4) RETURNING id`

	var id uuid.UUID
	err := r.db.QueryRow(ctx, q, userID, bookID, notes, priority).Scan(&id)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return nil, ErrAlreadyWanted
		case "23503":
			return nil, ErrNotFound
		}
	}
	if err != nil {
		return nil, fmt.Errorf("adding to wishlist: %w", err)
	}
	return r.FindByID(ctx, id)
}

// AddFreeText records a want for something the catalogue has never heard of:
// a book seen in a shop window, an out-of-print title with no ISBN.
func (r *WishlistRepo) AddFreeText(ctx context.Context, userID uuid.UUID, title, author, notes string, priority int) (*models.WishlistEntry, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		// The database enforces that a row names something; saying so here
		// makes it a message rather than a constraint violation.
		return nil, fmt.Errorf("a wishlist entry needs a title or a book")
	}

	const q = `
		INSERT INTO wishlist (user_id, title, author_name, notes, priority)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`

	var id uuid.UUID
	if err := r.db.QueryRow(ctx, q, userID, title, nullIfEmpty(author), notes, priority).Scan(&id); err != nil {
		return nil, fmt.Errorf("adding to wishlist: %w", err)
	}
	return r.FindByID(ctx, id)
}

func (r *WishlistRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.WishlistEntry, error) {
	q := `SELECT ` + wishlistColumns + `
	        FROM wishlist w LEFT JOIN books b ON b.id = w.book_id
	       WHERE w.id = $1`

	var e models.WishlistEntry
	err := r.db.QueryRow(ctx, q, id).Scan(&e.ID, &e.UserID, &e.BookID, &e.Title,
		&e.AuthorName, &e.Notes, &e.Priority, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding wishlist entry: %w", err)
	}
	return &e, nil
}

// Resolve attaches a free-text want to a catalogue entry once the book turns
// up, clearing the text it was standing in for.
//
// The row keeps its identity through this, which is the point of one table:
// the want does not become a different want when the catalogue catches up.
func (r *WishlistRepo) Resolve(ctx context.Context, id, bookID uuid.UUID) (*models.WishlistEntry, error) {
	const q = `
		UPDATE wishlist
		   SET book_id = $2, title = NULL, author_name = NULL
		 WHERE id = $1 AND book_id IS NULL`

	tag, err := r.db.Exec(ctx, q, id, bookID)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return nil, ErrAlreadyWanted
		case "23503":
			return nil, ErrNotFound
		}
	}
	if err != nil {
		return nil, fmt.Errorf("resolving wishlist entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.FindByID(ctx, id)
}

// Remove drops a want, which is what happens when the book arrives.
func (r *WishlistRepo) Remove(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM wishlist WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("removing wishlist entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
