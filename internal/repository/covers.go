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

type CoverRepo struct {
	db *pgxpool.Pool
}

func NewCoverRepo(db *pgxpool.Pool) *CoverRepo {
	return &CoverRepo{db: db}
}

// FindPrimary returns the primary cover for an entity, or ErrNotFound.
func (r *CoverRepo) FindPrimary(ctx context.Context, entityType string, entityID uuid.UUID) (*models.CoverImage, error) {
	const q = `
		SELECT id, entity_type, entity_id, filename, mime_type,
		       COALESCE(file_size, 0), is_primary, COALESCE(source_url, ''),
		       created_by, created_at
		FROM cover_images
		WHERE entity_type = $1 AND entity_id = $2 AND is_primary = true
		LIMIT 1`
	return scanCover(r.db.QueryRow(ctx, q, entityType, entityID))
}

// Insert saves a new cover record.
func (r *CoverRepo) Insert(ctx context.Context, c *models.CoverImage) error {
	const q = `
		INSERT INTO cover_images
		    (id, entity_type, entity_id, filename, mime_type, file_size, is_primary, source_url, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8,''), $9, $10)`
	_, err := r.db.Exec(ctx, q,
		c.ID, c.EntityType, c.EntityID, c.Filename, c.MimeType, c.FileSize,
		c.IsPrimary, c.SourceURL, c.CreatedBy, c.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting cover: %w", err)
	}
	return nil
}

// DeleteByEntityID removes all covers for an entity and returns the deleted filenames.
func (r *CoverRepo) DeleteByEntityID(ctx context.Context, entityType string, entityID uuid.UUID) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`DELETE FROM cover_images WHERE entity_type = $1 AND entity_id = $2 RETURNING filename`,
		entityType, entityID,
	)
	if err != nil {
		return nil, fmt.Errorf("deleting covers: %w", err)
	}
	defer rows.Close()
	var filenames []string
	for rows.Next() {
		var fn string
		if err := rows.Scan(&fn); err != nil {
			return nil, err
		}
		filenames = append(filenames, fn)
	}
	return filenames, rows.Err()
}

// ─── Scanner ──────────────────────────────────────────────────────────────────

func scanCover(row pgx.Row) (*models.CoverImage, error) {
	var (
		pgID        pgtype.UUID
		pgEntityID  pgtype.UUID
		pgCreatedBy pgtype.UUID
		c           models.CoverImage
	)
	err := row.Scan(
		&pgID, &c.EntityType, &pgEntityID, &c.Filename, &c.MimeType,
		&c.FileSize, &c.IsPrimary, &c.SourceURL, &pgCreatedBy, &c.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning cover: %w", err)
	}
	c.ID = uuid.UUID(pgID.Bytes)
	c.EntityID = uuid.UUID(pgEntityID.Bytes)
	if pgCreatedBy.Valid {
		id := uuid.UUID(pgCreatedBy.Bytes)
		c.CreatedBy = &id
	}
	return &c, nil
}
