// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/fireball1725/librarium-api/internal/models"
)

// InventoryCopy is a copy with enough of its book to show it on a shelf:
// the title, the authors and whether there's a cover.
type InventoryCopy struct {
	models.Copy
	BookTitle     string
	BookAuthors   string
	HasCover      bool
	BookUpdatedAt time.Time
}

// InventoryFilter narrows a library's copies to one place, or to the copies
// with no place. Neither set means every copy.
type InventoryFilter struct {
	LocationID *uuid.UUID
	Unshelved  bool
	// Inside takes the places inside LocationID too, so a room or a bookcase
	// shows what's on its shelves rather than only what's filed on it.
	Inside bool
}

// InventorySummary counts a library's copies for the Inventory page.
type InventorySummary struct {
	Copies    int `json:"copies"`
	Shelved   int `json:"shelved"`
	Unshelved int `json:"unshelved"`
	OnLoan    int `json:"on_loan"`
}

// ListForInventory returns a library's copies with their books, and how many
// match in all. A place's copies come by title, since that's how a shelf is
// read; unshelved and all copies come newest first, which is the to-do order.
func (r *CopyRepo) ListForInventory(ctx context.Context, libraryID uuid.UUID, f InventoryFilter, limit, offset int) ([]*InventoryCopy, int, error) {
	where := `c.library_id = $1 AND c.deleted_at IS NULL`
	args := []any{libraryID}
	order := `c.created_at DESC, c.id`
	switch {
	case f.LocationID != nil:
		args = append(args, *f.LocationID)
		if f.Inside {
			// The place and everything nested under it, however deep. The
			// depth cap stops a parent loop from running forever; the API
			// prevents those, but a database is older than its rules.
			where += fmt.Sprintf(` AND c.location_id IN (
				WITH RECURSIVE inside AS (
					SELECT id, 0 AS depth FROM copy_locations WHERE id = $%d
					UNION ALL
					SELECT l.id, inside.depth + 1 FROM copy_locations l
					  JOIN inside ON l.parent_id = inside.id
					 WHERE inside.depth < 16
				) SELECT id FROM inside)`, len(args))
		} else {
			where += fmt.Sprintf(` AND c.location_id = $%d`, len(args))
		}
		order = `lower(b.title), c.id`
	case f.Unshelved:
		where += ` AND c.location_id IS NULL`
	}

	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM copies c WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting inventory: %w", err)
	}

	args = append(args, limit, offset)
	q := `SELECT ` + copyColumns + `,
		b.title,
		COALESCE((
			SELECT string_agg(ct.name, ', ' ORDER BY bc.display_order)
			  FROM book_contributors bc
			  JOIN contributors ct ON ct.id = bc.contributor_id
			 WHERE bc.book_id = b.id AND bc.role = 'author'
		), ''),
		EXISTS(
			SELECT 1 FROM cover_images ci
			 WHERE ci.entity_type = 'book' AND ci.entity_id = b.id AND ci.is_primary = true
		),
		b.updated_at` + copyFrom + `
		JOIN books b ON b.id = c.book_id
		WHERE ` + where + `
		ORDER BY ` + order + fmt.Sprintf(` LIMIT $%d OFFSET $%d`, len(args)-1, len(args))

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing inventory: %w", err)
	}
	defer rows.Close()
	out := make([]*InventoryCopy, 0)
	for rows.Next() {
		ic, err := scanInventoryCopy(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scanning inventory: %w", err)
		}
		out = append(out, ic)
	}
	return out, total, rows.Err()
}

func scanInventoryCopy(row pgx.Row) (*InventoryCopy, error) {
	var ic InventoryCopy
	c := &ic.Copy
	err := row.Scan(
		&c.ID, &c.LibraryID, &c.BookID, &c.EditionID,
		&c.AcquiredAt, &c.AcquiredFrom, &c.AcquiredBy,
		&c.PriceMinor, &c.PriceCurrency,
		&c.Condition, &c.IsSigned, &c.Notes,
		&c.LocationID, &c.LocationName,
		&c.OnLoanTo,
		&c.CreatedAt, &c.UpdatedAt, &c.DeletedAt,
		&ic.BookTitle, &ic.BookAuthors, &ic.HasCover, &ic.BookUpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &ic, nil
}

// SummaryForInventory counts a library's copies: all, on a shelf, with no
// place, and out on loan.
func (r *CopyRepo) SummaryForInventory(ctx context.Context, libraryID uuid.UUID) (InventorySummary, error) {
	const q = `
		SELECT count(*),
		       count(*) FILTER (WHERE c.location_id IS NULL),
		       count(*) FILTER (WHERE EXISTS (
		           SELECT 1 FROM loans ln
		            WHERE ln.copy_id = c.id AND ln.returned_at IS NULL AND ln.deleted_at IS NULL))
		  FROM copies c
		 WHERE c.library_id = $1 AND c.deleted_at IS NULL`
	var s InventorySummary
	if err := r.db.QueryRow(ctx, q, libraryID).Scan(&s.Copies, &s.Unshelved, &s.OnLoan); err != nil {
		return s, fmt.Errorf("summarising inventory: %w", err)
	}
	s.Shelved = s.Copies - s.Unshelved
	return s, nil
}
