// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fireball1725/librarium-api/internal/config"
	"github.com/fireball1725/librarium-api/internal/db"
)

// runRepair checks whether schema_migrations is telling the truth and, if it is
// not, puts it right. It returns the process exit code.
//
// Migrate rewinds its own failures, so a database this build has always managed
// cannot need this. It is here for databases that were repaired by hand before
// that shipped: setting the version to the migration that just failed, rather
// than the one before it, marks a migration as applied when it rolled back, and
// nothing in the schema disagrees loudly enough to notice until a later
// migration reaches for a table that was never created.
//
// It reads first and prints what it found. Applying needs a yes, because the
// evidence is what the schema contains rather than a record of what ran:
//
//	docker compose run --rm api repair
func runRepair(args []string) int {
	fs := flag.NewFlagSet("repair", flag.ContinueOnError)
	dsn := fs.String("database-url", "", "Postgres connection string (defaults to DATABASE_URL)")
	yes := fs.Bool("yes", false, "apply the repair without asking, for non-interactive use")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	url := *dsn
	if url == "" {
		url = config.Load().DatabaseURL
	}
	if url == "" {
		_, _ = fmt.Fprintln(os.Stderr, "repair: no database URL; pass --database-url or set DATABASE_URL")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, err := db.Connect(ctx, url)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "repair: connecting: %v\n", err)
		return 1
	}
	defer pool.Close()

	plan, err := db.AnalyseRepair(ctx, pool)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "repair: %v\n", err)
		return 1
	}
	plan.Report(os.Stdout)

	if !plan.NeedsRepair() {
		return 0
	}
	if !plan.Confident {
		return 1
	}

	if !*yes && !confirm(os.Stdin, os.Stdout) {
		_, _ = fmt.Fprintln(os.Stdout, "Nothing written.")
		return 1
	}

	if err := db.ApplyRepair(url, plan); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "repair: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintln(os.Stdout, "\nDone. Start the server and the outstanding migrations will run.")
	return 0
}

// confirm asks before writing. A pipe with no --yes is a no rather than a
// silent yes, because the whole point of this command is that someone looked.
func confirm(in *os.File, out *os.File) bool {
	info, err := in.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		_, _ = fmt.Fprintln(out, "\nRe-run with --yes to apply this, once the plan above looks right.")
		return false
	}

	_, _ = fmt.Fprint(out, "\nApply it? [y/N] ")
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		return false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}
