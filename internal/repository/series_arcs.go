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

type SeriesArcRepo struct {
	db *pgxpool.Pool
}

func NewSeriesArcRepo(db *pgxpool.Pool) *SeriesArcRepo {
	return &SeriesArcRepo{db: db}
}

func (r *SeriesArcRepo) List(ctx context.Context, seriesID uuid.UUID) ([]*models.SeriesArc, error) {
	const q = `
		SELECT a.id, a.series_id, a.name, COALESCE(a.description, ''),
		       a.position, a.created_at, a.updated_at,
		       (SELECT COUNT(*) FROM book_series bs WHERE bs.arc_id = a.id) AS book_count
		FROM series_arcs a
		WHERE a.series_id = $1
		ORDER BY a.position, a.name`
	rows, err := r.db.Query(ctx, q, seriesID)
	if err != nil {
		return nil, fmt.Errorf("listing series arcs: %w", err)
	}
	defer rows.Close()

	out := []*models.SeriesArc{}
	for rows.Next() {
		arc, err := scanSeriesArc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, arc)
	}
	return out, rows.Err()
}

func (r *SeriesArcRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.SeriesArc, error) {
	const q = `
		SELECT a.id, a.series_id, a.name, COALESCE(a.description, ''),
		       a.position, a.created_at, a.updated_at,
		       (SELECT COUNT(*) FROM book_series bs WHERE bs.arc_id = a.id) AS book_count
		FROM series_arcs a
		WHERE a.id = $1`
	arc, err := scanSeriesArc(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding series arc: %w", err)
	}
	return arc, nil
}

func (r *SeriesArcRepo) Create(ctx context.Context, id, seriesID uuid.UUID, name, description string, position float64) (*models.SeriesArc, error) {
	const q = `
		INSERT INTO series_arcs (id, series_id, name, description, position)
		VALUES ($1, $2, $3, NULLIF($4,''), $5)`
	if _, err := r.db.Exec(ctx, q, id, seriesID, name, description, position); err != nil {
		return nil, fmt.Errorf("inserting series arc: %w", err)
	}
	return r.FindByID(ctx, id)
}

func (r *SeriesArcRepo) Update(ctx context.Context, id uuid.UUID, name, description string, position float64) (*models.SeriesArc, error) {
	const q = `
		UPDATE series_arcs
		SET name        = $2,
		    description = NULLIF($3, ''),
		    position    = $4,
		    updated_at  = NOW()
		WHERE id = $1`
	result, err := r.db.Exec(ctx, q, id, name, description, position)
	if err != nil {
		return nil, fmt.Errorf("updating series arc: %w", err)
	}
	if result.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.FindByID(ctx, id)
}

func (r *SeriesArcRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `DELETE FROM series_arcs WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting series arc: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetBookArc assigns or unassigns a book within a series to/from an arc. Pass
// arcID = nil to clear the assignment. Returns ErrNotFound when the (series,
// book) pair is not in book_series.
func (r *SeriesArcRepo) SetBookArc(ctx context.Context, seriesID, bookID uuid.UUID, arcID *uuid.UUID) error {
	result, err := r.db.Exec(ctx,
		`UPDATE book_series SET arc_id = $3 WHERE series_id = $1 AND book_id = $2`,
		seriesID, bookID, arcID,
	)
	if err != nil {
		return fmt.Errorf("setting book arc: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanSeriesArc(s scanner) (*models.SeriesArc, error) {
	var (
		pgID       pgtype.UUID
		pgSeriesID pgtype.UUID
		arc        models.SeriesArc
	)
	err := s.Scan(
		&pgID, &pgSeriesID, &arc.Name, &arc.Description,
		&arc.Position, &arc.CreatedAt, &arc.UpdatedAt,
		&arc.BookCount,
	)
	if err != nil {
		return nil, err
	}
	arc.ID = uuid.UUID(pgID.Bytes)
	arc.SeriesID = uuid.UUID(pgSeriesID.Bytes)
	return &arc, nil
}
