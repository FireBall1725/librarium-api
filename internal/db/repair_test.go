// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package db

import (
	"context"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrateTo brings a scratch database to one version, so a test can start from
// a database that is genuinely mid-upgrade rather than one that pretends to be.
func migrateTo(t *testing.T, dsn string, version uint) {
	t.Helper()

	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("creating migration source: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", source, toPgx5URL(dsn))
	if err != nil {
		t.Fatalf("creating migrator: %v", err)
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Migrate(version); err != nil {
		t.Fatalf("migrating to %d: %v", version, err)
	}
}

func recordVersion(t *testing.T, dsn string, version int, dirty bool) {
	t.Helper()

	ctx := context.Background()
	pool, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx,
		`UPDATE schema_migrations SET version = $1, dirty = $2`, version, dirty); err != nil {
		t.Fatalf("setting schema_migrations to %d/%t: %v", version, dirty, err)
	}
}

func readVersion(t *testing.T, dsn string) (int, bool) {
	t.Helper()

	ctx := context.Background()
	pool, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer pool.Close()

	version, dirty, err := readRecordedVersion(ctx, pool)
	if err != nil {
		t.Fatalf("reading version: %v", err)
	}
	return version, dirty
}

// TestMigrateRetriesAnInterruptedMigration covers a container killed partway
// through an upgrade. The migration rolled back with its transaction, so the
// only thing standing between the database and a working server is a dirty
// flag, and clearing that is not something anyone should be asked to do by
// hand.
func TestMigrateRetriesAnInterruptedMigration(t *testing.T) {
	withScratchDatabase(t, func(dsn string) {
		head, err := headVersion()
		if err != nil {
			t.Fatalf("reading head version: %v", err)
		}

		migrateTo(t, dsn, 24)
		// What a kill -9 during migration 25 leaves behind.
		recordVersion(t, dsn, 25, true)

		if err := Migrate(dsn); err != nil {
			t.Fatalf("Migrate should have recovered from a dirty version: %v", err)
		}

		version, dirty := readVersion(t, dsn)
		if version != head || dirty {
			t.Errorf("after recovery: version %d dirty %t, want %d false", version, dirty, head)
		}
	})
}

// TestRepairRecoversAHandEditedVersion is the failure a self-hoster reported on
// 29 August 2026: migration 25 failed, the dirty flag was cleared in place
// rather than rewound, and the next start ran migration 26 against a database
// with none of the tables 25 creates.
func TestRepairRecoversAHandEditedVersion(t *testing.T) {
	withScratchDatabase(t, func(dsn string) {
		ctx := context.Background()
		head, err := headVersion()
		if err != nil {
			t.Fatalf("reading head version: %v", err)
		}

		migrateTo(t, dsn, 24)
		// Clearing the flag without moving the version marks a migration that
		// rolled back as applied.
		recordVersion(t, dsn, 25, false)

		// Booting now fails, and the message has to point somewhere useful.
		err = Migrate(dsn)
		if err == nil {
			t.Fatal("Migrate succeeded against a database missing everything migration 25 creates")
		}
		for _, want := range []string{"copies", "migration 25", "repair"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error does not mention %q: %v", want, err)
			}
		}

		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatalf("connecting: %v", err)
		}
		defer pool.Close()

		plan, err := AnalyseRepair(ctx, pool)
		if err != nil {
			t.Fatalf("analysing: %v", err)
		}
		if !plan.NeedsRepair() {
			t.Fatal("repair found nothing to do")
		}
		if !plan.Confident {
			t.Fatalf("repair was not confident: %s", plan.Summary)
		}
		if plan.Target != 24 {
			t.Errorf("target = %d, want 24", plan.Target)
		}

		if err := ApplyRepair(dsn, plan); err != nil {
			t.Fatalf("applying: %v", err)
		}
		if err := Migrate(dsn); err != nil {
			t.Fatalf("migrating after repair: %v", err)
		}

		version, dirty := readVersion(t, dsn)
		if version != head || dirty {
			t.Errorf("after repair: version %d dirty %t, want %d false", version, dirty, head)
		}
	})
}

// TestRepairLeavesAHealthyDatabaseAlone is the case that has to stay boring:
// running repair on a database with nothing wrong must not write anything.
func TestRepairLeavesAHealthyDatabaseAlone(t *testing.T) {
	withScratchDatabase(t, func(dsn string) {
		ctx := context.Background()
		if err := Migrate(dsn); err != nil {
			t.Fatalf("migrating: %v", err)
		}

		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatalf("connecting: %v", err)
		}
		defer pool.Close()

		plan, err := AnalyseRepair(ctx, pool)
		if err != nil {
			t.Fatalf("analysing: %v", err)
		}
		if plan.NeedsRepair() {
			t.Errorf("repair wants to change a healthy database: %s", plan.Summary)
		}
	})
}

// TestMigrateRefusesADatabaseFromANewerBuild is the other half of the same
// report: after the hand edit they rolled back to the release, which ships
// fewer migrations than the version recorded.
func TestMigrateRefusesADatabaseFromANewerBuild(t *testing.T) {
	withScratchDatabase(t, func(dsn string) {
		head, err := headVersion()
		if err != nil {
			t.Fatalf("reading head version: %v", err)
		}

		if err := Migrate(dsn); err != nil {
			t.Fatalf("migrating: %v", err)
		}
		recordVersion(t, dsn, head+7, false)

		err = Migrate(dsn)
		if err == nil {
			t.Fatal("Migrate accepted a database from a newer build")
		}
		if !strings.Contains(err.Error(), "newer release") {
			t.Errorf("error should say the database came from a newer release, got: %v", err)
		}
		if version, _ := readVersion(t, dsn); version != head+7 {
			t.Errorf("refusing changed the version to %d; it must not write anything", version)
		}
	})
}

// TestMigrationSurvivesLegacyReadStatus reproduces the upgrade failure a
// self-hoster hit on 30 August 2026. user_book_interactions.read_status is a
// VARCHAR(32) with a default and no CHECK, so an empty string has always been
// legal there. user_books constrains the column, and migration 25's backfill
// used to copy whatever it found, so a single empty value stopped the upgrade
// with a constraint violation partway through a 900-line migration.
func TestMigrationSurvivesLegacyReadStatus(t *testing.T) {
	withScratchDatabase(t, func(dsn string) {
		ctx := context.Background()
		head, err := headVersion()
		if err != nil {
			t.Fatalf("reading head version: %v", err)
		}

		// Stop before the tier migration, while the old table is still the
		// only place reading state lives.
		migrateTo(t, dsn, 24)

		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatalf("connecting: %v", err)
		}
		defer pool.Close()

		userID := seedLegacyInteractions(ctx, t, pool)

		if err := Migrate(dsn); err != nil {
			t.Fatalf("migrating over legacy read_status values: %v", err)
		}
		if version, dirty := readVersion(t, dsn); version != head || dirty {
			t.Errorf("version %d dirty %t, want %d false", version, dirty, head)
		}

		// The empty status becomes unread rather than being dropped, and a real
		// status recorded on another edition of the same work still wins.
		rows, err := pool.Query(ctx, `
			SELECT b.title, ub.read_status
			  FROM user_books ub JOIN books b ON b.id = ub.book_id
			 WHERE ub.user_id = $1 ORDER BY b.title`, userID)
		if err != nil {
			t.Fatalf("reading user_books: %v", err)
		}
		defer rows.Close()

		got := map[string]string{}
		for rows.Next() {
			var title, status string
			if err := rows.Scan(&title, &status); err != nil {
				t.Fatalf("scanning: %v", err)
			}
			got[title] = status
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterating: %v", err)
		}

		want := map[string]string{
			"blank status":      "unread",
			"blank and read":    "read",
			"nonsense status":   "unread",
			"legitimate status": "reading",
		}
		for title, wantStatus := range want {
			if got[title] != wantStatus {
				t.Errorf("%q: read_status = %q, want %q", title, got[title], wantStatus)
			}
		}
	})
}

// seedLegacyInteractions writes the shapes a pre-tier collection can hold,
// including the empty read_status that no constraint ever prevented.
func seedLegacyInteractions(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()

	var mediaTypeID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types ORDER BY name LIMIT 1`).Scan(&mediaTypeID); err != nil {
		t.Fatalf("no media types seeded: %v", err)
	}

	userID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email)
		VALUES ($1, $2, 'legacy reader', $3)`,
		userID, "legacy-"+userID.String(), userID.String()+"@example.test"); err != nil {
		t.Fatalf("creating user: %v", err)
	}

	// title -> the statuses recorded across that book's editions.
	books := map[string][]string{
		"blank status":      {""},
		"blank and read":    {"", "read"},
		"nonsense status":   {"finished-ish"},
		"legitimate status": {"reading"},
	}

	for title, statuses := range books {
		bookID := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO books (id, title, media_type_id) VALUES ($1, $2, $3)`,
			bookID, title, mediaTypeID); err != nil {
			t.Fatalf("creating book %q: %v", title, err)
		}
		for _, status := range statuses {
			editionID := uuid.New()
			if _, err := pool.Exec(ctx, `
				INSERT INTO book_editions (id, book_id, format) VALUES ($1, $2, 'paperback')`,
				editionID, bookID); err != nil {
				t.Fatalf("creating edition for %q: %v", title, err)
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO user_book_interactions (id, user_id, book_edition_id, read_status)
				VALUES ($1, $2, $3, $4)`, uuid.New(), userID, editionID, status); err != nil {
				t.Fatalf("creating interaction for %q: %v", title, err)
			}
		}
	}

	return userID
}
