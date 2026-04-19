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

type StorageLocationRepo struct {
	db *pgxpool.Pool
}

func NewStorageLocationRepo(db *pgxpool.Pool) *StorageLocationRepo {
	return &StorageLocationRepo{db: db}
}

const storageLocationColumns = `
	id, library_id, name, root_path, media_format, COALESCE(path_template,''), created_at, updated_at
`

func (r *StorageLocationRepo) List(ctx context.Context, libraryID uuid.UUID) ([]*models.StorageLocation, error) {
	q := `SELECT ` + storageLocationColumns + `FROM storage_locations WHERE library_id = $1 ORDER BY name`
	rows, err := r.db.Query(ctx, q, libraryID)
	if err != nil {
		return nil, fmt.Errorf("listing storage locations: %w", err)
	}
	defer rows.Close()
	var out []*models.StorageLocation
	for rows.Next() {
		loc, err := scanStorageLocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, loc)
	}
	return out, rows.Err()
}

func (r *StorageLocationRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.StorageLocation, error) {
	q := `SELECT ` + storageLocationColumns + `FROM storage_locations WHERE id = $1`
	loc, err := scanStorageLocation(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding storage location: %w", err)
	}
	return loc, nil
}

func (r *StorageLocationRepo) Create(ctx context.Context, id, libraryID uuid.UUID, name, rootPath, mediaFormat, pathTemplate string) (*models.StorageLocation, error) {
	const q = `
		INSERT INTO storage_locations (id, library_id, name, root_path, media_format, path_template)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + storageLocationColumns
	loc, err := scanStorageLocation(r.db.QueryRow(ctx, q, id, libraryID, name, rootPath, mediaFormat, pathTemplate))
	if err != nil {
		return nil, fmt.Errorf("creating storage location: %w", err)
	}
	return loc, nil
}

func (r *StorageLocationRepo) Update(ctx context.Context, id uuid.UUID, name, rootPath, mediaFormat, pathTemplate string) (*models.StorageLocation, error) {
	const q = `
		UPDATE storage_locations
		SET name = $2, root_path = $3, media_format = $4, path_template = $5
		WHERE id = $1
		RETURNING ` + storageLocationColumns
	loc, err := scanStorageLocation(r.db.QueryRow(ctx, q, id, name, rootPath, mediaFormat, pathTemplate))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("updating storage location: %w", err)
	}
	return loc, nil
}

func (r *StorageLocationRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `DELETE FROM storage_locations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting storage location: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanStorageLocation(s scanner) (*models.StorageLocation, error) {
	var (
		pgID        pgtype.UUID
		pgLibraryID pgtype.UUID
		loc         models.StorageLocation
	)
	err := s.Scan(&pgID, &pgLibraryID, &loc.Name, &loc.RootPath, &loc.MediaFormat, &loc.PathTemplate, &loc.CreatedAt, &loc.UpdatedAt)
	if err != nil {
		return nil, err
	}
	loc.ID = uuid.UUID(pgID.Bytes)
	loc.LibraryID = uuid.UUID(pgLibraryID.Bytes)
	return &loc, nil
}
