// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Connect opens a pgxpool connection and verifies it with a ping.
func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing database URL: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return pool, nil
}

// Migrate runs all pending up migrations embedded in the binary.
func Migrate(databaseURL string) error {
	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("creating migration source: %w", err)
	}

	// golang-migrate's pgx/v5 driver expects the pgx5:// scheme.
	migrateURL := toPgx5URL(databaseURL)

	m, err := migrate.NewWithSourceInstance("iofs", source, migrateURL)
	if err != nil {
		return fmt.Errorf("creating migrator: %w", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			slog.Error("closing migration source", "error", srcErr)
		}
		if dbErr != nil {
			slog.Error("closing migration db connection", "error", dbErr)
		}
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		// If the DB is dirty (a previous migration was interrupted), force-reset
		// the dirty flag to the current version so Up() can retry.
		var dirtyErr migrate.ErrDirty
		if errors.As(err, &dirtyErr) {
			slog.Warn("dirty migration state detected — forcing version to retry", "version", dirtyErr.Version)
			if ferr := m.Force(dirtyErr.Version); ferr != nil {
				return fmt.Errorf("forcing dirty migration version: %w", ferr)
			}
			if rerr := m.Up(); rerr != nil && !errors.Is(rerr, migrate.ErrNoChange) {
				return fmt.Errorf("running migrations after force: %w", rerr)
			}
		} else {
			return fmt.Errorf("running migrations: %w", err)
		}
	}

	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("reading migration version: %w", err)
	}
	slog.Info("migrations up to date", "version", version, "dirty", dirty)

	return nil
}

// toPgx5URL converts a postgres:// or postgresql:// URL to the pgx5:// scheme
// required by golang-migrate's pgx/v5 driver.
func toPgx5URL(u string) string {
	u = strings.Replace(u, "postgresql://", "pgx5://", 1)
	u = strings.Replace(u, "postgres://", "pgx5://", 1)
	return u
}
