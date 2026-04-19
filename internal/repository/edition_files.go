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

type EditionFileRepo struct {
	db *pgxpool.Pool
}

func NewEditionFileRepo(db *pgxpool.Pool) *EditionFileRepo {
	return &EditionFileRepo{db: db}
}

const efColumns = `
	id, edition_id, file_format, COALESCE(file_name,''), file_path,
	storage_location_id, file_size, display_order, created_at
`

func scanEditionFile(s scanner) (*models.EditionFile, error) {
	var (
		pgID                pgtype.UUID
		pgEditionID         pgtype.UUID
		pgStorageLocationID pgtype.UUID
		pgFileSize          pgtype.Int8
		ef                  models.EditionFile
	)
	err := s.Scan(
		&pgID, &pgEditionID, &ef.FileFormat, &ef.FileName, &ef.FilePath,
		&pgStorageLocationID, &pgFileSize, &ef.DisplayOrder, &ef.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	ef.ID = uuid.UUID(pgID.Bytes)
	ef.EditionID = uuid.UUID(pgEditionID.Bytes)
	if pgStorageLocationID.Valid {
		id := uuid.UUID(pgStorageLocationID.Bytes)
		ef.StorageLocationID = &id
	}
	if pgFileSize.Valid {
		v := pgFileSize.Int64
		ef.FileSize = &v
	}
	return &ef, nil
}

// ListByEdition returns all files attached to an edition, ordered by display_order.
func (r *EditionFileRepo) ListByEdition(ctx context.Context, editionID uuid.UUID) ([]*models.EditionFile, error) {
	q := `SELECT ` + efColumns + `FROM edition_files WHERE edition_id = $1 ORDER BY display_order, created_at`
	rows, err := r.db.Query(ctx, q, editionID)
	if err != nil {
		return nil, fmt.Errorf("listing edition files: %w", err)
	}
	defer rows.Close()
	var out []*models.EditionFile
	for rows.Next() {
		ef, err := scanEditionFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ef)
	}
	return out, rows.Err()
}

// ListByEditions batch-fetches files for a set of editions, returning a map of editionID → files.
func (r *EditionFileRepo) ListByEditions(ctx context.Context, editionIDs []uuid.UUID) (map[uuid.UUID][]*models.EditionFile, error) {
	if len(editionIDs) == 0 {
		return nil, nil
	}
	q := `SELECT ` + efColumns + `FROM edition_files WHERE edition_id = ANY($1) ORDER BY edition_id, display_order, created_at`
	rows, err := r.db.Query(ctx, q, editionIDs)
	if err != nil {
		return nil, fmt.Errorf("batch listing edition files: %w", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID][]*models.EditionFile)
	for rows.Next() {
		ef, err := scanEditionFile(rows)
		if err != nil {
			return nil, err
		}
		out[ef.EditionID] = append(out[ef.EditionID], ef)
	}
	return out, rows.Err()
}

// FindByID returns a single edition file by its ID.
func (r *EditionFileRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.EditionFile, error) {
	q := `SELECT ` + efColumns + `FROM edition_files WHERE id = $1`
	ef, err := scanEditionFile(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding edition file: %w", err)
	}
	return ef, nil
}

// Add inserts a new edition file record. The display_order is auto-assigned as MAX+1.
func (r *EditionFileRepo) Add(ctx context.Context, ef *models.EditionFile) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO edition_files
			(id, edition_id, file_format, file_name, file_path, storage_location_id, file_size, display_order)
		VALUES
			($1, $2, $3, NULLIF($4,''), $5, $6, $7,
			 COALESCE((SELECT MAX(display_order)+1 FROM edition_files WHERE edition_id = $2), 0))`,
		ef.ID, ef.EditionID, ef.FileFormat, ef.FileName, ef.FilePath, ef.StorageLocationID, ef.FileSize,
	)
	if err != nil {
		return fmt.Errorf("adding edition file: %w", err)
	}
	return nil
}

// Delete removes an edition file by ID.
func (r *EditionFileRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `DELETE FROM edition_files WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting edition file: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
