// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fireball1725/librarium-api/internal/providers"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EditionAnswer is one provider's stored answer for an edition.
type EditionAnswer struct {
	Provider  string                `json:"provider"`
	LookupKey string                `json:"lookup_key"`
	FetchedAt time.Time             `json:"fetched_at"`
	Result    *providers.BookResult `json:"result"`
}

// EditionAnswerRepo keeps every provider's answer for an edition, so a field
// can be switched later without asking the provider again.
type EditionAnswerRepo struct {
	db *pgxpool.Pool
}

func NewEditionAnswerRepo(db *pgxpool.Pool) *EditionAnswerRepo {
	return &EditionAnswerRepo{db: db}
}

// Save stores each answer, replacing that provider's earlier one for the
// edition and leaving other providers' answers alone.
func (r *EditionAnswerRepo) Save(ctx context.Context, editionID uuid.UUID, lookupKey string, answers []*providers.BookResult) error {
	const q = `
		INSERT INTO edition_provider_answers (edition_id, provider, lookup_key, fetched_at, result)
		VALUES ($1, $2, $3, NOW(), $4)
		ON CONFLICT (edition_id, provider)
		DO UPDATE SET lookup_key = EXCLUDED.lookup_key, fetched_at = EXCLUDED.fetched_at, result = EXCLUDED.result`
	for _, a := range answers {
		if a == nil || a.Provider == "" {
			continue
		}
		raw, err := json.Marshal(a)
		if err != nil {
			return fmt.Errorf("encoding %s answer: %w", a.Provider, err)
		}
		if _, err := r.db.Exec(ctx, q, editionID, a.Provider, lookupKey, raw); err != nil {
			return fmt.Errorf("saving %s answer: %w", a.Provider, err)
		}
	}
	return nil
}

// ListForEdition returns every stored answer, oldest first, which is close to
// the order the providers first answered in. Never nil.
func (r *EditionAnswerRepo) ListForEdition(ctx context.Context, editionID uuid.UUID) ([]*EditionAnswer, error) {
	const q = `
		SELECT provider, lookup_key, fetched_at, result
		  FROM edition_provider_answers
		 WHERE edition_id = $1
		 ORDER BY fetched_at, provider`
	rows, err := r.db.Query(ctx, q, editionID)
	if err != nil {
		return nil, fmt.Errorf("listing answers: %w", err)
	}
	defer rows.Close()

	out := make([]*EditionAnswer, 0)
	for rows.Next() {
		var (
			a   EditionAnswer
			raw []byte
		)
		if err := rows.Scan(&a.Provider, &a.LookupKey, &a.FetchedAt, &raw); err != nil {
			return nil, fmt.Errorf("scanning answer: %w", err)
		}
		if err := json.Unmarshal(raw, &a.Result); err != nil {
			return nil, fmt.Errorf("decoding %s answer: %w", a.Provider, err)
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}
