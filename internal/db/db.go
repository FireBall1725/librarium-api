// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
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
//
// Anything that fails here stops the server, so the states that used to need
// someone with psql are handled here instead:
//
//   - A migration that failed leaves schema_migrations dirty. golang-migrate's
//     pgx driver sends each file to the server as one simple query, and Postgres
//     wraps a multi-statement simple query in an implicit transaction, so a
//     failed migration rolled back in full and the schema is exactly as it was.
//     Rewinding the version so the file runs again is therefore safe, and doing
//     it here means the next boot shows the error that actually caused the
//     failure rather than a dirty flag. That matters because the intuitive way
//     to clear a dirty flag is to clear the flag, and clearing it in place marks
//     the failed migration as applied: the next boot resumes at the migration
//     after it, which then fails on tables its predecessor never created.
//
//   - A database migrated by a newer build than the one now running. Left to
//     golang-migrate that surfaces as a missing file for a version it has never
//     heard of, which sends people looking for a packaging bug.
//
// What is not handled here is a version that was already set by hand before
// this build shipped. Deciding that needs evidence from the schema and a person
// who can say yes, so it lives in the repair subcommand.
func Migrate(databaseURL string) error {
	head, err := headVersion()
	if err != nil {
		return err
	}

	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("creating migration source: %w", err)
	}

	// golang-migrate's pgx/v5 driver expects the pgx5:// scheme.
	m, err := migrate.NewWithSourceInstance("iofs", source, toPgx5URL(databaseURL))
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

	if err := refuseIfAhead(m, head); err != nil {
		return err
	}

	if err := runUp(m); err != nil {
		return err
	}

	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("reading migration version: %w", err)
	}
	slog.Info("migrations up to date", "version", version, "dirty", dirty)

	return nil
}

// refuseIfAhead stops a build from running against a database that a later one
// has already migrated. Nothing here can fix that, but saying so beats the
// error golang-migrate would give, which names a file rather than the image.
func refuseIfAhead(m *migrate.Migrate, head int) error {
	version, _, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading migration version: %w", err)
	}
	if int(version) <= head {
		return nil
	}

	return fmt.Errorf(
		"this database is at migration version %d and this build ships %d: it was migrated by a newer "+
			"release than the one now running. Start the image that last migrated it, or restore a backup "+
			"taken before that upgrade", version, head)
}

// runUp applies everything pending, recovering from a run that was interrupted
// and rewinding its own failure so the next attempt starts from a clean state.
func runUp(m *migrate.Migrate) error {
	err := m.Up()

	// Dirty on entry means a previous process started a migration and was
	// killed before it could finish or tidy up. The file rolled back with its
	// transaction, so let it run again.
	var dirty migrate.ErrDirty
	if errors.As(err, &dirty) {
		if rerr := rewind(m, dirty.Version, "was interrupted"); rerr != nil {
			return rerr
		}
		err = m.Up()
	}

	if err == nil || errors.Is(err, migrate.ErrNoChange) {
		return nil
	}

	// The migration that just failed left the version dirty. Put it back before
	// returning, so what anyone sees on the next boot is the error below.
	failed := unknownVersion
	if version, isDirty, verr := m.Version(); verr == nil && isDirty {
		failed = int(version)
		if rerr := rewind(m, failed, "failed"); rerr != nil {
			return errors.Join(explainFailure(failed, err), rerr)
		}
	}

	return explainFailure(failed, err)
}

// unknownVersion is used when a failure cannot be pinned to a migration.
const unknownVersion = -1

// migrationFailure keeps the original error reachable while presenting
// something a person can read. golang-migrate renders a failure by printing the
// entire migration file, which for the schema-tiers migration is 900 lines of
// SQL wrapped in one JSON log field, and the sentence that matters is the last
// one.
type migrationFailure struct {
	msg string
	err error
}

func (e migrationFailure) Error() string { return e.msg }
func (e migrationFailure) Unwrap() error { return e.err }

// explainFailure turns a migration error into the four things worth knowing:
// which migration, which line, what Postgres said, and whether the cause is a
// version that claims a migration which never ran.
func explainFailure(version int, err error) error {
	var dbErr database.Error
	pgErr, ok := asPgError(err, &dbErr)
	if !ok {
		return fmt.Errorf("running migrations: %w", err)
	}

	name := "unknown"
	if source, found, srcErr := migrationByVersion(version); srcErr == nil && found {
		name = source.Name
	}

	var b strings.Builder
	fmt.Fprintf(&b, "migration %d (%s) failed", version, name)
	if dbErr.Line > 0 {
		fmt.Fprintf(&b, " at line %d", dbErr.Line)
	}
	fmt.Fprintf(&b, ": %s (SQLSTATE %s)", pgErr.Message, pgErr.Code)

	if stmt := sourceLine(dbErr.Query, dbErr.Line); stmt != "" {
		fmt.Fprintf(&b, "\n    %s", stmt)
	}
	if pgErr.Detail != "" {
		fmt.Fprintf(&b, "\n%s", pgErr.Detail)
	}
	if pgErr.Hint != "" {
		fmt.Fprintf(&b, "\n%s", pgErr.Hint)
	}
	if hint := missingObjectHint(pgErr); hint != "" {
		fmt.Fprintf(&b, "\n\n%s", hint)
	}

	return migrationFailure{msg: b.String(), err: err}
}

// asPgError digs the Postgres error out of golang-migrate's wrapper, which does
// not implement Unwrap.
func asPgError(err error, dbErr *database.Error) (*pgconn.PgError, bool) {
	if !errors.As(err, dbErr) {
		return nil, false
	}
	pgErr, ok := dbErr.OrigErr.(*pgconn.PgError)
	return pgErr, ok
}

// sourceLine pulls one line out of the migration body, so the report shows the
// statement that failed rather than the file that contained it.
func sourceLine(query []byte, line uint) string {
	if line == 0 {
		return ""
	}
	lines := strings.Split(string(query), "\n")
	if int(line) > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[line-1])
}

// rewind sets the version back to the one before an unfinished migration, so
// that migration runs again. It refuses when the file could have left something
// behind, because then the schema and the version disagree in a way only a
// person with the backup can settle.
func rewind(m *migrate.Migrate, version int, why string) error {
	source, ok, err := migrationByVersion(version)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf(
			"migration %d %s but is not in this build, so it cannot be retried: start the image that "+
				"last migrated this database", version, why)
	}

	if reasons := atomicityBlockers(source.SQL); len(reasons) > 0 {
		return fmt.Errorf(
			"migration %d (%s) %s and cannot be retried automatically because %s. Part of it may have "+
				"applied, so the schema may not match what schema_migrations reports. Restore the backup "+
				"taken before the upgrade", version, source.Name, why, strings.Join(reasons, "; "))
	}

	target := version - 1
	if target < 1 {
		target = NoVersion
	}
	if err := m.Force(target); err != nil {
		return fmt.Errorf("rewinding schema_migrations from %d to %s: %w", version, versionLabel(target), err)
	}

	slog.Warn("rolled back an unfinished migration; it runs again on the next start",
		"migration", version, "name", source.Name, "reason", why, "version_set_to", versionLabel(target))
	return nil
}

// quotedName pulls the first quoted identifier out of a Postgres error message.
// The message text is translated when lc_messages is set; the identifier inside
// it is not.
var quotedName = regexp.MustCompile(`"([^"]+)"`)

// missingObjectHint adds the one thing Postgres cannot know: which migration
// was supposed to create the object that is missing. A database whose version
// was set by hand always fails this way, on a later migration reaching for
// something its predecessor never created.
func missingObjectHint(pgErr *pgconn.PgError) string {
	switch pgErr.Code {
	case pgerrcode.UndefinedTable, pgerrcode.UndefinedColumn,
		pgerrcode.UndefinedFunction, pgerrcode.UndefinedObject:
	default:
		return ""
	}

	match := quotedName.FindStringSubmatch(pgErr.Message)
	if match == nil {
		return ""
	}
	creator, found, err := migrationThatCreates(match[1])
	if err != nil || !found {
		return ""
	}

	return fmt.Sprintf(
		"%q is created by migration %d (%s), which schema_migrations reports as already applied. "+
			"A version that was set by hand looks exactly like this. Run `librarium-api repair` to see "+
			"which migrations actually ran.", match[1], creator.Version, creator.Name)
}

// migrationThatCreates finds the earliest migration that creates a named object.
func migrationThatCreates(name string) (migrationSource, bool, error) {
	ups, err := upMigrations()
	if err != nil {
		return migrationSource{}, false, err
	}
	for _, source := range ups {
		for _, p := range probes(source.SQL) {
			if p.kind != probeColumn && p.target == name {
				return source, true, nil
			}
			if p.kind == probeColumn && p.column == name {
				return source, true, nil
			}
		}
	}
	return migrationSource{}, false, nil
}

// toPgx5URL converts a postgres:// or postgresql:// URL to the pgx5:// scheme
// required by golang-migrate's pgx/v5 driver.
func toPgx5URL(u string) string {
	u = strings.Replace(u, "postgresql://", "pgx5://", 1)
	u = strings.Replace(u, "postgres://", "pgx5://", 1)
	return u
}
