// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrContainmentCycle is returned when a link would make a work contain itself,
// directly or through a chain.
//
// This is not a tidiness rule. visible_books and effective_read_status both walk
// book_contents recursively, and a cycle never terminates.
var ErrContainmentCycle = errors.New("that would make a work contain itself")

type BookContentsRepo struct {
	db *pgxpool.Pool
}

func NewBookContentsRepo(db *pgxpool.Pool) *BookContentsRepo {
	return &BookContentsRepo{db: db}
}

// ListContents returns what a container holds, in reading order.
func (r *BookContentsRepo) ListContents(ctx context.Context, containerID uuid.UUID) ([]*models.BookContent, error) {
	const q = `
		SELECT bc.container_id, bc.contained_id, bc.position, b.title
		  FROM book_contents bc
		  JOIN books b ON b.id = bc.contained_id
		 WHERE bc.container_id = $1
		 ORDER BY bc.position, b.title`

	return r.scanContents(ctx, q, containerID)
}

// ListContainers returns the works that contain this one.
//
// The reverse direction is what answers "I own the 3-in-1, so do I have volume
// 2?". Ownership resolves through containment, so this is the lookup behind a
// series gap list not showing a volume you actually have on the shelf.
func (r *BookContentsRepo) ListContainers(ctx context.Context, containedID uuid.UUID) ([]*models.BookContent, error) {
	const q = `
		SELECT bc.container_id, bc.contained_id, bc.position, b.title
		  FROM book_contents bc
		  JOIN books b ON b.id = bc.container_id
		 WHERE bc.contained_id = $1
		 ORDER BY b.title`

	return r.scanContents(ctx, q, containedID)
}

func (r *BookContentsRepo) scanContents(ctx context.Context, q string, arg uuid.UUID) ([]*models.BookContent, error) {
	rows, err := r.db.Query(ctx, q, arg)
	if err != nil {
		return nil, fmt.Errorf("listing contents: %w", err)
	}
	defer rows.Close()

	out := make([]*models.BookContent, 0)
	for rows.Next() {
		var c models.BookContent
		if err := rows.Scan(&c.ContainerID, &c.ContainedID, &c.Position, &c.Title); err != nil {
			return nil, fmt.Errorf("scanning content link: %w", err)
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// Add records that a container holds a work at a position.
//
// Position is numeric so an omnibus can hold something at 7.5 without a
// migration, matching series_volumes.
func (r *BookContentsRepo) Add(ctx context.Context, containerID, containedID uuid.UUID, position float64) error {
	if containerID == containedID {
		return ErrContainmentCycle
	}

	// The database rejects the direct case with a CHECK, but the transitive one
	// needs asking: if the container is already somewhere inside the work being
	// added, this link closes a loop. Checked before the insert rather than
	// after, because the recursive walk that would detect it later is the same
	// walk that would not terminate.
	cycle, err := r.reaches(ctx, containedID, containerID)
	if err != nil {
		return err
	}
	if cycle {
		return ErrContainmentCycle
	}

	const q = `
		INSERT INTO book_contents (container_id, contained_id, position)
		VALUES ($1, $2, $3)
		ON CONFLICT (container_id, contained_id) DO UPDATE SET position = EXCLUDED.position`

	_, err = r.db.Exec(ctx, q, containerID, containedID, position)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23514": // check_violation, the no-self constraint
			return ErrContainmentCycle
		case "23503": // foreign_key_violation, one of the works is gone
			return ErrNotFound
		}
	}
	if err != nil {
		return fmt.Errorf("adding content link: %w", err)
	}
	return nil
}

// Remove drops a link. Removing one that is not there is not an error.
func (r *BookContentsRepo) Remove(ctx context.Context, containerID, containedID uuid.UUID) error {
	const q = `DELETE FROM book_contents WHERE container_id = $1 AND contained_id = $2`
	if _, err := r.db.Exec(ctx, q, containerID, containedID); err != nil {
		return fmt.Errorf("removing content link: %w", err)
	}
	return nil
}

// reaches reports whether target is anywhere inside root, following containment
// downward. Depth is bounded so a cycle that somehow already exists in the data
// cannot hang this query too.
func (r *BookContentsRepo) reaches(ctx context.Context, root, target uuid.UUID) (bool, error) {
	const q = `
		WITH RECURSIVE walk AS (
		        SELECT contained_id, 1 AS depth
		          FROM book_contents
		         WHERE container_id = $1
		    UNION
		        SELECT bc.contained_id, w.depth + 1
		          FROM walk w
		          JOIN book_contents bc ON bc.container_id = w.contained_id
		         WHERE w.depth < 32)
		SELECT EXISTS (SELECT 1 FROM walk WHERE contained_id = $2)`

	var found bool
	if err := r.db.QueryRow(ctx, q, root, target).Scan(&found); err != nil {
		return false, fmt.Errorf("checking containment cycle: %w", err)
	}
	return found, nil
}
