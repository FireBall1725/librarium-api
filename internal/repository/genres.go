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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type GenreRepo struct {
	db *pgxpool.Pool
}

func NewGenreRepo(db *pgxpool.Pool) *GenreRepo {
	return &GenreRepo{db: db}
}

func (r *GenreRepo) List(ctx context.Context) ([]*models.Genre, error) {
	const q = `SELECT id, name, created_at FROM genres ORDER BY name`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("listing genres: %w", err)
	}
	defer rows.Close()

	var out []*models.Genre
	for rows.Next() {
		g, err := scanGenre(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *GenreRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Genre, error) {
	const q = `SELECT id, name, created_at FROM genres WHERE id = $1`
	g, err := scanGenre(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding genre: %w", err)
	}
	return g, nil
}

func (r *GenreRepo) Create(ctx context.Context, id uuid.UUID, name string) (*models.Genre, error) {
	const q = `
		INSERT INTO genres (id, name)
		VALUES ($1, $2)
		RETURNING id, name, created_at`
	g, err := scanGenre(r.db.QueryRow(ctx, q, id, name))
	if err != nil {
		return nil, fmt.Errorf("inserting genre: %w", err)
	}
	return g, nil
}

func (r *GenreRepo) Update(ctx context.Context, id uuid.UUID, name string) (*models.Genre, error) {
	const q = `
		UPDATE genres SET name = $2
		WHERE id = $1
		RETURNING id, name, created_at`
	g, err := scanGenre(r.db.QueryRow(ctx, q, id, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("updating genre: %w", err)
	}
	return g, nil
}

func (r *GenreRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `DELETE FROM genres WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting genre: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MatchNames returns genres whose name_lower matches any of the provided names
// (case-insensitive). Useful for normalizing provider categories.
func (r *GenreRepo) MatchNames(ctx context.Context, names []string) ([]*models.Genre, error) {
	if len(names) == 0 {
		return nil, nil
	}
	lower := make([]string, len(names))
	for i, n := range names {
		lower[i] = strings.ToLower(n)
	}
	const q = `SELECT id, name, created_at FROM genres WHERE name_lower = ANY($1) ORDER BY name`
	rows, err := r.db.Query(ctx, q, lower)
	if err != nil {
		return nil, fmt.Errorf("matching genre names: %w", err)
	}
	defer rows.Close()

	var out []*models.Genre
	for rows.Next() {
		g, err := scanGenre(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetBookGenres replaces all genres on a book within the given transaction.
func (r *GenreRepo) SetBookGenres(ctx context.Context, tx pgx.Tx, bookID uuid.UUID, genreIDs []uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM book_genres WHERE book_id = $1`, bookID); err != nil {
		return fmt.Errorf("clearing book genres: %w", err)
	}
	for _, gid := range genreIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO book_genres (book_id, genre_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			bookID, gid,
		); err != nil {
			return fmt.Errorf("inserting book genre: %w", err)
		}
	}
	return nil
}

func scanGenre(s scanner) (*models.Genre, error) {
	var pgID pgtype.UUID
	var g models.Genre
	if err := s.Scan(&pgID, &g.Name, &g.CreatedAt); err != nil {
		return nil, err
	}
	g.ID = uuid.UUID(pgID.Bytes)
	return &g, nil
}
