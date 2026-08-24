// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package db

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

var migrationName = regexp.MustCompile(`^(\d{6})_([a-z0-9_]+)\.(up|down)\.sql$`)

// TestMigrationFilesAreWellFormed needs no database, so it runs everywhere and
// catches the mistakes that are cheap to make and expensive to discover on a
// live instance: a missing down file, a duplicated sequence number, a gap that
// makes golang-migrate skip silently.
func TestMigrationFilesAreWellFormed(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("reading embedded migrations: %v", err)
	}

	ups := map[int]string{}
	downs := map[int]string{}
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			t.Errorf("%s does not match NNNNNN_name.{up,down}.sql", e.Name())
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Errorf("%s: unparseable sequence number: %v", e.Name(), err)
			continue
		}
		target := ups
		if m[3] == "down" {
			target = downs
		}
		if prev, dup := target[n]; dup {
			t.Errorf("sequence %06d used twice: %s and %s", n, prev, e.Name())
		}
		target[n] = e.Name()
	}

	if len(ups) == 0 {
		t.Fatal("found no up migrations; the embed pattern is probably wrong")
	}

	// Every up needs a down. AGENTS.md is explicit that the down has to actually
	// reverse the up, and where it cannot it should say so and refuse rather
	// than silently lose data. A missing file is neither.
	for n, name := range ups {
		if _, ok := downs[n]; !ok {
			t.Errorf("%s has no matching .down.sql", name)
		}
	}
	for n, name := range downs {
		if _, ok := ups[n]; !ok {
			t.Errorf("%s has no matching .up.sql", name)
		}
	}

	// Contiguous from 1. A gap means golang-migrate's notion of "next" skips a
	// number, which is fine until someone later fills the gap and every
	// already-migrated database ignores it.
	nums := make([]int, 0, len(ups))
	for n := range ups {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	for i, n := range nums {
		if want := i + 1; n != want {
			t.Errorf("migration sequence jumps: expected %06d, found %06d (%s)", want, n, ups[n])
			break
		}
	}
}

// TestMigrationsApplyToEmptyDatabase runs the real migrator against a scratch
// database created for this test and dropped afterwards. It exists because
// nothing else in the repo ever executes a migration: the live tests skip
// without a DSN and CI never sets one, so until now a migration reached a real
// instance without having been run once.
//
// Point LIBRARIUM_MIGRATE_TEST_DSN at any Postgres the test may create and drop
// databases on. It is deliberately not LIBRARIUM_TEST_DSN, which points at a
// restored copy of a real collection and must not be written to.
func TestMigrationsApplyToEmptyDatabase(t *testing.T) {
	adminDSN := os.Getenv("LIBRARIUM_MIGRATE_TEST_DSN")
	if adminDSN == "" {
		t.Skip("set LIBRARIUM_MIGRATE_TEST_DSN to run")
	}

	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	scratch := fmt.Sprintf("librarium_migtest_%d_%d", os.Getpid(), time.Now().UnixNano()%1e6)
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{scratch}.Sanitize()); err != nil {
		t.Fatalf("creating scratch database: %v", err)
	}
	defer func() {
		// Terminate stragglers first; the migrator's pool may not have closed
		// yet and DROP DATABASE refuses while anything is connected.
		_, _ = admin.Exec(ctx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, scratch)
		if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{scratch}.Sanitize()); err != nil {
			t.Logf("dropping scratch database %s: %v", scratch, err)
		}
	}()

	scratchDSN, err := replaceDatabase(adminDSN, scratch)
	if err != nil {
		t.Fatalf("building scratch DSN: %v", err)
	}

	if err := Migrate(scratchDSN); err != nil {
		t.Fatalf("migrating an empty database: %v", err)
	}

	conn, err := pgx.Connect(ctx, scratchDSN)
	if err != nil {
		t.Fatalf("connecting to scratch database: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	// The head on disk is the only correct answer, so derive it rather than
	// hardcoding a number this test would then need updating for.
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("reading embedded migrations: %v", err)
	}
	wantVersion := 0
	for _, e := range entries {
		if m := migrationName.FindStringSubmatch(e.Name()); m != nil && m[3] == "up" {
			if n, _ := strconv.Atoi(m[1]); n > wantVersion {
				wantVersion = n
			}
		}
	}

	var version int
	var dirty bool
	if err := conn.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		t.Fatalf("reading schema_migrations: %v", err)
	}
	if dirty {
		t.Error("migrations finished dirty, which should now be impossible: Migrate refuses to continue past a dirty state")
	}
	if version != wantVersion {
		t.Errorf("schema_migrations version = %d, want %d (the highest up migration on disk)", version, wantVersion)
	}

	// A handful of invariants, chosen because each has actually been wrong at
	// some point rather than to pad the count.
	var roles, permissions, mediaTypes int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM roles`).Scan(&roles); err != nil {
		t.Fatalf("counting roles: %v", err)
	}
	if roles == 0 {
		t.Error("no roles seeded; RequireLibraryPermission has nothing to resolve against")
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM permissions`).Scan(&permissions); err != nil {
		t.Fatalf("counting permissions: %v", err)
	}
	if permissions == 0 {
		t.Error("no permissions seeded")
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM media_types`).Scan(&mediaTypes); err != nil {
		t.Fatalf("counting media types: %v", err)
	}
	if mediaTypes == 0 {
		t.Error("no media types seeded; 000008 refuses to backfill floating books without one")
	}

	// Every permission granted to a role must exist. A typo here means a role
	// silently grants nothing, which is the failure mode that is hardest to see
	// from the admin UI.
	var orphanGrants int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM role_permissions rp
		WHERE NOT EXISTS (SELECT 1 FROM permissions p WHERE p.id = rp.permission_id)`).Scan(&orphanGrants); err != nil {
		t.Fatalf("checking role_permissions: %v", err)
	}
	if orphanGrants != 0 {
		t.Errorf("%d role_permissions rows reference a permission that does not exist", orphanGrants)
	}
}

// replaceDatabase swaps the database name in a postgres DSN, keeping every
// other component. Used to point at the scratch database without asking the
// caller to supply two connection strings that have to agree.
func replaceDatabase(dsn, name string) (string, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "", fmt.Errorf("parsing DSN: %w", err)
	}
	host := cfg.Host
	if strings.HasPrefix(host, "/") {
		// Unix socket paths need to travel as a query parameter, not a host.
		return fmt.Sprintf("postgres:///%s?host=%s&user=%s&password=%s",
			name, host, cfg.User, cfg.Password), nil
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		cfg.User, cfg.Password, host, cfg.Port, name), nil
}
