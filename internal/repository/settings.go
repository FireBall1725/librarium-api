// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SettingsRepo struct {
	db *pgxpool.Pool
}

func NewSettingsRepo(db *pgxpool.Pool) *SettingsRepo {
	return &SettingsRepo{db: db}
}

// Get returns the value for a key, or ("", ErrNotFound) if missing.
func (r *SettingsRepo) Get(ctx context.Context, key string) (string, error) {
	const q = `SELECT value FROM instance_settings WHERE key = $1`
	var val string
	err := r.db.QueryRow(ctx, q, key).Scan(&val)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("settings get %q: %w", key, err)
	}
	return val, nil
}

// Set upserts a key-value pair.
func (r *SettingsRepo) Set(ctx context.Context, key, value string) error {
	const q = `
		INSERT INTO instance_settings (key, value, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`
	_, err := r.db.Exec(ctx, q, key, value, time.Now())
	if err != nil {
		return fmt.Errorf("settings set %q: %w", key, err)
	}
	return nil
}

// GetByPrefix returns all settings whose key starts with prefix.
func (r *SettingsRepo) GetByPrefix(ctx context.Context, prefix string) (map[string]string, error) {
	const q = `SELECT key, value FROM instance_settings WHERE key LIKE $1 ORDER BY key`
	rows, err := r.db.Query(ctx, q, strings.ReplaceAll(prefix, "%", "\\%")+"%")
	if err != nil {
		return nil, fmt.Errorf("settings prefix %q: %w", prefix, err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
