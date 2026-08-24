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
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReadStatuses is the closed vocabulary the application branches on, so it
// stays here rather than becoming a table: adding one needs a code change
// anyway, and a table would turn a compile-time gap into a runtime one.
var ReadStatuses = []string{"unread", "reading", "read", "did_not_finish"}

func validReadStatus(s string) bool {
	for _, v := range ReadStatuses {
		if v == s {
			return true
		}
	}
	return false
}

type UserBookRepo struct {
	db *pgxpool.Pool
}

func NewUserBookRepo(db *pgxpool.Pool) *UserBookRepo {
	return &UserBookRepo{db: db}
}

const userBookColumns = `
	user_id, book_id, read_status, rating, is_favorite, review, notes, wants,
	read_status_updated_at, rating_updated_at, is_favorite_updated_at,
	created_at, updated_at, deleted_at`

func scanUserBook(row pgx.Row) (*models.UserBook, error) {
	var u models.UserBook
	err := row.Scan(
		&u.UserID, &u.BookID, &u.ReadStatus, &u.Rating, &u.IsFavorite,
		&u.Review, &u.Notes, &u.Wants,
		&u.ReadStatusUpdatedAt, &u.RatingUpdatedAt, &u.IsFavoriteUpdatedAt,
		&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// Get returns what a person thinks of one work, or ErrNotFound when they have
// said nothing about it.
func (r *UserBookRepo) Get(ctx context.Context, userID, bookID uuid.UUID) (*models.UserBook, error) {
	q := `SELECT ` + userBookColumns + `
	        FROM user_books
	       WHERE user_id = $1 AND book_id = $2 AND deleted_at IS NULL`

	u, err := scanUserBook(r.db.QueryRow(ctx, q, userID, bookID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting reading state: %w", err)
	}
	return u, nil
}

// GetEffective returns reading state including what is inherited from a
// container this person has read.
//
// Owning and reading a 3-in-1 means volumes 1 to 3 have been read, and this is
// where that surfaces. Only 'read' inherits: being midway through an omnibus
// does not make its third volume in progress. An explicit row always wins, so a
// volume marked did_not_finish stays that way.
func (r *UserBookRepo) GetEffective(ctx context.Context, userID, bookID uuid.UUID) (*models.UserBook, error) {
	const q = `
		SELECT user_id, book_id, read_status, inherited
		  FROM effective_read_status
		 WHERE user_id = $1 AND book_id = $2`

	var u models.UserBook
	err := r.db.QueryRow(ctx, q, userID, bookID).
		Scan(&u.UserID, &u.BookID, &u.ReadStatus, &u.Inherited)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting effective reading state: %w", err)
	}

	// An inherited row carries a status and nothing else, because a rating is
	// an opinion about the thing rated and never moves. A direct row has the
	// rest, so fetch it.
	if !u.Inherited {
		full, err := r.Get(ctx, userID, bookID)
		if err != nil {
			return nil, err
		}
		return full, nil
	}
	return &u, nil
}

// UpsertInput carries only what the caller is changing. Every field is a
// pointer so leaving one nil means "do not touch", which is what makes a
// partial update from one client safe while another client owns other fields.
type UpsertInput struct {
	ReadStatus *string
	Rating     **int
	IsFavorite *bool
	Review     *string
	Notes      *string
	Wants      *bool
}

// Upsert writes reading state for a work, creating the row if this is the first
// thing anyone has said about it.
//
// The per-field timestamps move only for the fields that actually changed,
// which is what makes last-writer-wins sync resolve per field rather than
// per row: two clients editing different fields both win.
func (r *UserBookRepo) Upsert(ctx context.Context, userID, bookID uuid.UUID, in UpsertInput) (*models.UserBook, error) {
	if in.ReadStatus != nil && !validReadStatus(*in.ReadStatus) {
		return nil, fmt.Errorf("unknown read status %q", *in.ReadStatus)
	}

	const q = `
		INSERT INTO user_books (user_id, book_id, read_status, rating, is_favorite,
		                        review, notes, wants,
		                        read_status_updated_at, rating_updated_at, is_favorite_updated_at)
		VALUES ($1, $2,
		        COALESCE($3, 'unread'), $4, COALESCE($5, FALSE),
		        COALESCE($6, ''), COALESCE($7, ''), COALESCE($8, FALSE),
		        CASE WHEN $3 IS NOT NULL THEN NOW() END,
		        CASE WHEN $9 THEN NOW() END,
		        CASE WHEN $5 IS NOT NULL THEN NOW() END)
		ON CONFLICT (user_id, book_id) DO UPDATE SET
		    read_status = COALESCE($3, user_books.read_status),
		    rating      = CASE WHEN $9 THEN $4 ELSE user_books.rating END,
		    is_favorite = COALESCE($5, user_books.is_favorite),
		    review      = COALESCE($6, user_books.review),
		    notes       = COALESCE($7, user_books.notes),
		    wants       = COALESCE($8, user_books.wants),
		    read_status_updated_at = CASE WHEN $3 IS NOT NULL AND $3 IS DISTINCT FROM user_books.read_status
		                                 THEN NOW() ELSE user_books.read_status_updated_at END,
		    rating_updated_at      = CASE WHEN $9 AND $4 IS DISTINCT FROM user_books.rating
		                                 THEN NOW() ELSE user_books.rating_updated_at END,
		    is_favorite_updated_at = CASE WHEN $5 IS NOT NULL AND $5 IS DISTINCT FROM user_books.is_favorite
		                                 THEN NOW() ELSE user_books.is_favorite_updated_at END,
		    updated_at  = NOW(),
		    deleted_at  = NULL
		RETURNING ` + userBookColumns

	// ratingGiven distinguishes "clear the rating" from "leave it alone", which
	// a nullable column cannot express with one parameter.
	var rating *int
	ratingGiven := in.Rating != nil
	if ratingGiven {
		rating = *in.Rating
	}

	u, err := scanUserBook(r.db.QueryRow(ctx, q,
		userID, bookID, in.ReadStatus, rating, in.IsFavorite,
		in.Review, in.Notes, in.Wants, ratingGiven))
	if err != nil {
		return nil, fmt.Errorf("writing reading state: %w", err)
	}
	return u, nil
}

// Delete soft-deletes reading state. A review someone wrote is expensive to
// lose by accident, so the row survives and the history tables keep the text.
func (r *UserBookRepo) Delete(ctx context.Context, userID, bookID uuid.UUID) error {
	const q = `
		UPDATE user_books SET deleted_at = NOW(), updated_at = NOW()
		 WHERE user_id = $1 AND book_id = $2 AND deleted_at IS NULL`

	tag, err := r.db.Exec(ctx, q, userID, bookID)
	if err != nil {
		return fmt.Errorf("deleting reading state: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountsByStatus reports how many works sit at each status for one person,
// including statuses with nothing in them.
//
// Zero-filling matters: a view that shows no number is indistinguishable from
// one that failed to load, which is exactly the bug the facet rail had.
func (r *UserBookRepo) CountsByStatus(ctx context.Context, userID uuid.UUID) (map[string]int, error) {
	const q = `
		SELECT read_status, count(*)
		  FROM user_books
		 WHERE user_id = $1 AND deleted_at IS NULL
		 GROUP BY read_status`

	out := make(map[string]int, len(ReadStatuses))
	for _, s := range ReadStatuses {
		out[s] = 0
	}

	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("counting reading state: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scanning status count: %w", err)
		}
		out[status] = n
	}
	return out, rows.Err()
}
