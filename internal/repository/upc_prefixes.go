// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UPCPrefixRepo holds the UPC company to ISBN prefix pairings an instance
// learned from real books. See migration 43.
type UPCPrefixRepo struct {
	db *pgxpool.Pool
}

func NewUPCPrefixRepo(db *pgxpool.Pool) *UPCPrefixRepo {
	return &UPCPrefixRepo{db: db}
}

// ISBNPrefixes lists the ISBN prefixes learned for a UPC company, oldest
// first.
func (r *UPCPrefixRepo) ISBNPrefixes(ctx context.Context, upcPrefix string) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT isbn_prefix FROM upc_isbn_prefixes WHERE upc_prefix = $1 ORDER BY created_at, isbn_prefix`, upcPrefix)
	if err != nil {
		return nil, fmt.Errorf("listing upc prefixes: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scanning upc prefix: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Learn records a pairing. added is false when the instance already knew it.
func (r *UPCPrefixRepo) Learn(ctx context.Context, upcPrefix, isbnPrefix, isbn string, userID *uuid.UUID) (added bool, err error) {
	tag, err := r.db.Exec(ctx, `
		INSERT INTO upc_isbn_prefixes (upc_prefix, isbn_prefix, learned_from_isbn, learned_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (upc_prefix, isbn_prefix) DO NOTHING`,
		upcPrefix, isbnPrefix, isbn, userID)
	if err != nil {
		return false, fmt.Errorf("learning upc prefix: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
