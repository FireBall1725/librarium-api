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

// ErrLocationCycle is returned when a location would become its own ancestor.
// Locations are a tree so "office contains top shelf" needs no second concept,
// and a loop in that tree hangs anything that walks it.
var ErrLocationCycle = errors.New("that would make a location contain itself")

// ErrLocationInUse is returned when deleting a location that still holds copies.
// Refusing is better than orphaning: a copy whose location silently became null
// is a book you cannot find.
var ErrLocationInUse = errors.New("that location still holds copies")

type CopyLocationRepo struct {
	db *pgxpool.Pool
}

func NewCopyLocationRepo(db *pgxpool.Pool) *CopyLocationRepo {
	return &CopyLocationRepo{db: db}
}

// List returns a library's locations with a live count of what is filed at
// each, ordered so parents read before their children.
func (r *CopyLocationRepo) List(ctx context.Context, libraryID uuid.UUID) ([]*models.CopyLocation, error) {
	// Depth first, down a path built from the names on the way, so a place and
	// everything inside it stay together.
	//
	// This ordered by COALESCE(parent_id, id), which groups a node with its
	// siblings and nothing more: the key is the parent's UUID, which sorts
	// arbitrarily, and a grandchild sorts under its own parent's id with no
	// relation to where the grandparent landed. A room could appear between
	// another room and the shelf inside it.
	const q = `
		WITH RECURSIVE tree AS (
		    SELECT l.id, l.parent_id,
		           ARRAY[lower(l.name), l.id::text] AS path,
		           0 AS depth
		      FROM copy_locations l
		     WHERE l.library_id = $1 AND l.parent_id IS NULL
		  UNION ALL
		    SELECT c.id, c.parent_id,
		           t.path || lower(c.name) || c.id::text,
		           t.depth + 1
		      FROM copy_locations c
		      JOIN tree t ON c.parent_id = t.id
		     -- Bounded, for the same reason every walk of this tree is: a loop
		     -- is refused on write, and a bound is what stops one already in
		     -- the data from hanging the read.
		     WHERE c.library_id = $1 AND t.depth < 16
		)
		SELECT l.id, l.library_id, l.name, l.parent_id, l.created_at,
		       (SELECT count(*) FROM copies c
		         WHERE c.location_id = l.id AND c.deleted_at IS NULL)
		  FROM copy_locations l
		  -- LEFT, and unreachable rows sort last rather than vanishing. A node
		  -- inside a cycle has no root to descend from, and a place that cannot
		  -- be listed is a place nobody can delete to fix the cycle.
		  LEFT JOIN tree t ON t.id = l.id
		 WHERE l.library_id = $1
		 ORDER BY (t.path IS NULL), t.path, lower(l.name), l.id`

	rows, err := r.db.Query(ctx, q, libraryID)
	if err != nil {
		return nil, fmt.Errorf("listing locations: %w", err)
	}
	defer rows.Close()

	out := make([]*models.CopyLocation, 0)
	for rows.Next() {
		var l models.CopyLocation
		if err := rows.Scan(&l.ID, &l.LibraryID, &l.Name, &l.ParentID, &l.CreatedAt, &l.CopyCount); err != nil {
			return nil, fmt.Errorf("scanning location: %w", err)
		}
		out = append(out, &l)
	}
	return out, rows.Err()
}

func (r *CopyLocationRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.CopyLocation, error) {
	const q = `
		SELECT l.id, l.library_id, l.name, l.parent_id, l.created_at,
		       (SELECT count(*) FROM copies c WHERE c.location_id = l.id AND c.deleted_at IS NULL)
		  FROM copy_locations l WHERE l.id = $1`

	var l models.CopyLocation
	err := r.db.QueryRow(ctx, q, id).
		Scan(&l.ID, &l.LibraryID, &l.Name, &l.ParentID, &l.CreatedAt, &l.CopyCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding location: %w", err)
	}
	return &l, nil
}

func (r *CopyLocationRepo) Create(ctx context.Context, libraryID uuid.UUID, name string, parentID *uuid.UUID) (*models.CopyLocation, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("location name is required")
	}

	// A parent has to be in the same library, or a copy could be filed at a
	// shelf in someone else's house by way of its parent.
	if parentID != nil {
		parent, err := r.FindByID(ctx, *parentID)
		if err != nil {
			return nil, err
		}
		if parent.LibraryID != libraryID {
			return nil, ErrLocationNotInLibrary
		}
	}

	const q = `
		INSERT INTO copy_locations (library_id, name, parent_id)
		VALUES ($1, $2, $3) RETURNING id`

	var id uuid.UUID
	if err := r.db.QueryRow(ctx, q, libraryID, name, parentID).Scan(&id); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("creating location: %w", err)
	}
	return r.FindByID(ctx, id)
}

// Rename changes a location's name, and optionally its parent.
//
// Reparenting is where the cycle check earns its place: moving a location under
// one of its own descendants makes a loop that every tree walk falls into.
func (r *CopyLocationRepo) Rename(ctx context.Context, id uuid.UUID, name string, parentID *uuid.UUID, reparent bool) (*models.CopyLocation, error) {
	current, err := r.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if reparent && parentID != nil {
		if *parentID == id {
			return nil, ErrLocationCycle
		}
		parent, err := r.FindByID(ctx, *parentID)
		if err != nil {
			return nil, err
		}
		if parent.LibraryID != current.LibraryID {
			return nil, ErrLocationNotInLibrary
		}
		descendant, err := r.isDescendant(ctx, *parentID, id)
		if err != nil {
			return nil, err
		}
		if descendant {
			return nil, ErrLocationCycle
		}
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = current.Name
	}

	if reparent {
		const q = `UPDATE copy_locations SET name = $2, parent_id = $3 WHERE id = $1`
		if _, err := r.db.Exec(ctx, q, id, name, parentID); err != nil {
			return nil, fmt.Errorf("updating location: %w", err)
		}
	} else {
		const q = `UPDATE copy_locations SET name = $2 WHERE id = $1`
		if _, err := r.db.Exec(ctx, q, id, name); err != nil {
			return nil, fmt.Errorf("updating location: %w", err)
		}
	}
	return r.FindByID(ctx, id)
}

// Delete removes an empty location. Children are reparented to nothing by the
// schema's ON DELETE SET NULL, which flattens rather than cascading, because
// deleting a shelf should not delete the shelves inside it.
func (r *CopyLocationRepo) Delete(ctx context.Context, id uuid.UUID) error {
	var inUse int
	if err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM copies WHERE location_id = $1 AND deleted_at IS NULL`, id).Scan(&inUse); err != nil {
		return fmt.Errorf("checking location use: %w", err)
	}
	if inUse > 0 {
		return ErrLocationInUse
	}

	tag, err := r.db.Exec(ctx, `DELETE FROM copy_locations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting location: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// isDescendant reports whether candidate sits anywhere under root. Depth is
// bounded so a loop already in the data cannot hang the check meant to prevent
// loops.
func (r *CopyLocationRepo) isDescendant(ctx context.Context, candidate, root uuid.UUID) (bool, error) {
	const q = `
		WITH RECURSIVE up AS (
		        SELECT id, parent_id, 1 AS depth FROM copy_locations WHERE id = $1
		    UNION
		        SELECT l.id, l.parent_id, u.depth + 1
		          FROM up u JOIN copy_locations l ON l.id = u.parent_id
		         WHERE u.depth < 32)
		SELECT EXISTS (SELECT 1 FROM up WHERE id = $2)`

	var found bool
	if err := r.db.QueryRow(ctx, q, candidate, root).Scan(&found); err != nil {
		return false, fmt.Errorf("checking location ancestry: %w", err)
	}
	return found, nil
}
