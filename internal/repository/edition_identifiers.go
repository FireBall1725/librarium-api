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

// ErrIdentifierTaken is returned when a (scheme, value) pair already belongs to
// a different edition. The pair is the primary key, which is the uniqueness the
// dedup logic has always assumed and, before the tiers migration, never had.
var ErrIdentifierTaken = errors.New("identifier already claimed by another edition")

// ErrUnknownScheme is returned for a scheme with no row in identifier_schemes.
// Schemes are a vocabulary table so a new one is an INSERT rather than a
// migration, but it still has to exist before anything references it.
var ErrUnknownScheme = errors.New("unknown identifier scheme")

type EditionIdentifierRepo struct {
	db *pgxpool.Pool
}

func NewEditionIdentifierRepo(db *pgxpool.Pool) *EditionIdentifierRepo {
	return &EditionIdentifierRepo{db: db}
}

// ListSchemes returns the active identifier vocabulary in display order.
func (r *EditionIdentifierRepo) ListSchemes(ctx context.Context) ([]*models.IdentifierScheme, error) {
	const q = `
		SELECT code, sort_order, is_active
		  FROM identifier_schemes
		 WHERE is_active
		 ORDER BY sort_order, code`

	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("listing identifier schemes: %w", err)
	}
	defer rows.Close()

	// Initialised rather than nil: a nil slice marshals to null and the React
	// client crashes where it expects an array.
	out := make([]*models.IdentifierScheme, 0)
	for rows.Next() {
		var s models.IdentifierScheme
		if err := rows.Scan(&s.Code, &s.SortOrder, &s.IsActive); err != nil {
			return nil, fmt.Errorf("scanning identifier scheme: %w", err)
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// ListForEdition returns every identifier on one printing, ordered by the
// vocabulary's own sort order so ISBN-13 leads and a publisher catalogue number
// trails.
func (r *EditionIdentifierRepo) ListForEdition(ctx context.Context, editionID uuid.UUID) ([]*models.EditionIdentifier, error) {
	const q = `
		SELECT ei.edition_id, ei.scheme, ei.value
		  FROM edition_identifiers ei
		  JOIN identifier_schemes s ON s.code = ei.scheme
		 WHERE ei.edition_id = $1
		 ORDER BY s.sort_order, ei.scheme, ei.value`

	rows, err := r.db.Query(ctx, q, editionID)
	if err != nil {
		return nil, fmt.Errorf("listing identifiers: %w", err)
	}
	defer rows.Close()

	out := make([]*models.EditionIdentifier, 0)
	for rows.Next() {
		var e models.EditionIdentifier
		if err := rows.Scan(&e.EditionID, &e.Scheme, &e.Value); err != nil {
			return nil, fmt.Errorf("scanning identifier: %w", err)
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// FindEditionBy resolves an identifier to the edition that claims it.
//
// This is step one of the matching chain: an exact match on a scheme and value,
// with no judgement involved. Only when this finds nothing does anything fuzzier
// get considered, and title matching never does.
func (r *EditionIdentifierRepo) FindEditionBy(ctx context.Context, scheme, value string) (uuid.UUID, error) {
	const q = `SELECT edition_id FROM edition_identifiers WHERE scheme = $1 AND value = $2`

	var id uuid.UUID
	err := r.db.QueryRow(ctx, q, normaliseScheme(scheme), strings.TrimSpace(value)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("finding edition by identifier: %w", err)
	}
	return id, nil
}

// Add attaches an identifier to an edition.
//
// An edition may carry several identifiers, including more than one of a
// scheme: a reprint can pick up a second ISBN without becoming a different
// printing. What cannot happen is two editions claiming the same one.
func (r *EditionIdentifierRepo) Add(ctx context.Context, editionID uuid.UUID, scheme, value string) error {
	scheme = normaliseScheme(scheme)
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("identifier value is required")
	}

	const q = `INSERT INTO edition_identifiers (edition_id, scheme, value) VALUES ($1, $2, $3)`
	_, err := r.db.Exec(ctx, q, editionID, scheme, value)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation on the (scheme, value) primary key
			return ErrIdentifierTaken
		case "23503": // foreign_key_violation
			// Two different parents can be missing here, and telling them apart
			// is the difference between "add the scheme first" and "that
			// edition does not exist".
			if strings.Contains(pgErr.ConstraintName, "scheme") {
				return ErrUnknownScheme
			}
			return ErrNotFound
		}
	}
	if err != nil {
		return fmt.Errorf("adding identifier: %w", err)
	}
	return nil
}

// Remove detaches an identifier. Removing one that is not there is not an
// error: the caller asked for it to be gone and it is gone.
func (r *EditionIdentifierRepo) Remove(ctx context.Context, editionID uuid.UUID, scheme, value string) error {
	const q = `DELETE FROM edition_identifiers WHERE edition_id = $1 AND scheme = $2 AND value = $3`
	if _, err := r.db.Exec(ctx, q, editionID, normaliseScheme(scheme), strings.TrimSpace(value)); err != nil {
		return fmt.Errorf("removing identifier: %w", err)
	}
	return nil
}

// normaliseScheme lowercases and trims so callers can send "ISBN13" and match a
// row stored as "isbn13". Values are trimmed but never case-folded: an ISBN-10
// check digit is a capital X and folding it breaks the identifier.
func normaliseScheme(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
