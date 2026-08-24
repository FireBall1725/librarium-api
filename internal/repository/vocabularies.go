// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"fmt"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VocabularyRepo reads the controlled vocabularies: edition formats, copy
// conditions, contributor roles.
//
// These are tables rather than CHECK constraints so that adding a format is an
// INSERT rather than a migration and a release. That only pays off if clients
// can read them, which is what this exists for: a format dropdown built from a
// constant in the client is the same hardcoding one layer up.
type VocabularyRepo struct {
	db *pgxpool.Pool
}

func NewVocabularyRepo(db *pgxpool.Pool) *VocabularyRepo {
	return &VocabularyRepo{db: db}
}

// Vocabulary names are not user input: each is a constant below, and the query
// is built from that constant rather than from anything a caller sent.
const (
	VocabEditionFormats   = "edition_formats"
	VocabCopyConditions   = "copy_conditions"
	VocabContributorRoles = "contributor_roles"
)

func (r *VocabularyRepo) EditionFormats(ctx context.Context) ([]*models.Vocabulary, error) {
	return r.list(ctx, VocabEditionFormats, false)
}

func (r *VocabularyRepo) CopyConditions(ctx context.Context) ([]*models.Vocabulary, error) {
	return r.list(ctx, VocabCopyConditions, false)
}

func (r *VocabularyRepo) ContributorRoles(ctx context.Context) ([]*models.Vocabulary, error) {
	return r.list(ctx, VocabContributorRoles, true)
}

func (r *VocabularyRepo) list(ctx context.Context, table string, appliesTo bool) ([]*models.Vocabulary, error) {
	cols := "code, sort_order, is_active, ''"
	if appliesTo {
		cols = "code, sort_order, is_active, applies_to"
	}
	// Inactive rows are withheld rather than returned with a flag: a client
	// listing a vocabulary is filling a picker, and something retired should not
	// be offered. is_active still crosses the wire so a caller reading a stored
	// code knows what it is looking at.
	q := fmt.Sprintf(`SELECT %s FROM %s WHERE is_active ORDER BY sort_order, code`, cols, table)

	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", table, err)
	}
	defer rows.Close()

	// Initialised rather than nil: a nil slice marshals to null and the React
	// client crashes where it expects an array.
	out := make([]*models.Vocabulary, 0)
	for rows.Next() {
		var v models.Vocabulary
		if err := rows.Scan(&v.Code, &v.SortOrder, &v.IsActive, &v.AppliesTo); err != nil {
			return nil, fmt.Errorf("scanning %s: %w", table, err)
		}
		out = append(out, &v)
	}
	return out, rows.Err()
}
