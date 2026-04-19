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

type MediaTypeRepo struct {
	db *pgxpool.Pool
}

func NewMediaTypeRepo(db *pgxpool.Pool) *MediaTypeRepo {
	return &MediaTypeRepo{db: db}
}

// List returns all media types ordered by display name, including the number of
// books assigned to each type.
func (r *MediaTypeRepo) List(ctx context.Context) ([]*models.MediaType, error) {
	const q = `
		SELECT mt.id, mt.name, mt.display_name, COALESCE(mt.description, ''),
		       COUNT(b.id) AS book_count
		FROM   media_types mt
		LEFT JOIN books b ON b.media_type_id = mt.id
		GROUP BY mt.id
		ORDER BY mt.display_name`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("listing media types: %w", err)
	}
	defer rows.Close()

	var out []*models.MediaType
	for rows.Next() {
		mt, err := scanMediaType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, mt)
	}
	return out, rows.Err()
}

// Create inserts a new media type.
func (r *MediaTypeRepo) Create(ctx context.Context, id uuid.UUID, name, displayName, description string) (*models.MediaType, error) {
	const q = `
		INSERT INTO media_types (id, name, display_name, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, display_name, COALESCE(description, '')`
	var (
		pgID        pgtype.UUID
		mt          models.MediaType
	)
	err := r.db.QueryRow(ctx, q, id, name, displayName, description).Scan(
		&pgID, &mt.Name, &mt.DisplayName, &mt.Description,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting media type: %w", err)
	}
	mt.ID = uuid.UUID(pgID.Bytes)
	return &mt, nil
}

// Update changes the display name and description of a media type.
// The internal name is immutable.
func (r *MediaTypeRepo) Update(ctx context.Context, id uuid.UUID, displayName, description string) (*models.MediaType, error) {
	const q = `
		UPDATE media_types SET display_name = $2, description = $3
		WHERE id = $1
		RETURNING id, name, display_name, COALESCE(description, ''), 0::bigint`
	var (
		pgID pgtype.UUID
		mt   models.MediaType
	)
	err := r.db.QueryRow(ctx, q, id, displayName, description).Scan(
		&pgID, &mt.Name, &mt.DisplayName, &mt.Description, &mt.BookCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("updating media type: %w", err)
	}
	mt.ID = uuid.UUID(pgID.Bytes)
	return &mt, nil
}

// Delete removes a media type.  Returns ErrInUse if any books reference it,
// ErrNotFound if the id does not exist.
func (r *MediaTypeRepo) Delete(ctx context.Context, id uuid.UUID) error {
	var count int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM books WHERE media_type_id = $1`, id,
	).Scan(&count); err != nil {
		return fmt.Errorf("counting books for media type: %w", err)
	}
	if count > 0 {
		return ErrInUse
	}

	result, err := r.db.Exec(ctx, `DELETE FROM media_types WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting media type: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanMediaType(s scanner) (*models.MediaType, error) {
	var pgID pgtype.UUID
	var mt models.MediaType
	if err := s.Scan(&pgID, &mt.Name, &mt.DisplayName, &mt.Description, &mt.BookCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	mt.ID = uuid.UUID(pgID.Bytes)
	return &mt, nil
}
