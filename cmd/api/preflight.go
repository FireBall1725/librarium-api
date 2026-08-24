// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/fireball1725/librarium-api/internal/config"
	"github.com/fireball1725/librarium-api/internal/db"
	"github.com/fireball1725/librarium-api/internal/preflight"
)

// runPreflight reports what the schema migration would find, without changing
// anything. It returns the process exit code.
//
// This is a subcommand rather than an admin endpoint on purpose: migrations run
// at boot, so when one refuses there is no server left to ask. Run it against
// the new image before touching the running stack:
//
//	docker run --rm ghcr.io/fireball1725/librarium-api:NEW \
//	    preflight --database-url=postgres://...
func runPreflight(args []string) int {
	fs := flag.NewFlagSet("preflight", flag.ContinueOnError)
	dsn := fs.String("database-url", "", "Postgres connection string (defaults to DATABASE_URL)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	url := *dsn
	if url == "" {
		url = config.Load().DatabaseURL
	}
	if url == "" {
		fmt.Fprintln(os.Stderr, "preflight: no database URL; pass --database-url or set DATABASE_URL")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "preflight: connecting: %v\n", err)
		return 1
	}
	defer pool.Close()

	findings, err := preflight.Run(ctx, pool)
	if err != nil {
		fmt.Fprintf(os.Stderr, "preflight: %v\n", err)
		return 1
	}

	preflight.Report(os.Stdout, findings)

	// A non-zero exit lets this sit in a deploy script ahead of the upgrade.
	if preflight.Blocked(findings) {
		return 1
	}
	return 0
}
