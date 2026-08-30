// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package db

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repairing a database whose recorded version is a lie.
//
// schema_migrations holds one row: a version and a dirty flag. Nothing records
// how it got there, so a version set by hand is indistinguishable from one the
// migrator wrote. That matters because the intuitive way to clear a dirty flag
// is to clear the flag, and doing that in place marks the migration that just
// failed as applied. The next boot resumes at the following migration, which
// then fails on tables its predecessor never created.
//
// Migrate now rewinds instead of leaving a dirty flag around to tempt anyone,
// so new databases cannot reach that state. This exists for the ones that
// already did.
//
// The evidence is the objects each migration creates. Because a migration runs
// in one implicit transaction, a single missing object proves the whole file
// did not apply, whatever the recorded version says. Presence proves less:
// IF NOT EXISTS and OR REPLACE mean an object can predate the migration that
// mentions it. So the walk moves down from the recorded version while it can
// prove non-application, and stops at the first version it cannot, which is the
// version to rewind to.

// migrationName matches the NNNNNN_name.{up,down}.sql convention every file in
// migrations/ follows.
var migrationName = regexp.MustCompile(`^(\d{6})_([a-z0-9_]+)\.(up|down)\.sql$`)

// migrationSource is one embedded up migration.
type migrationSource struct {
	Version int
	Name    string
	SQL     string
}

// upMigrations reads every embedded up migration, ordered by version.
func upMigrations() ([]migrationSource, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("reading embedded migrations: %w", err)
	}

	var out []migrationSource
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil || m[3] != "up" {
			continue
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("%s: unparseable sequence number: %w", e.Name(), err)
		}
		body, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		out = append(out, migrationSource{Version: version, Name: m[2], SQL: string(body)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// headVersion is the highest migration this binary carries.
func headVersion() (int, error) {
	ups, err := upMigrations()
	if err != nil {
		return 0, err
	}
	if len(ups) == 0 {
		return 0, fmt.Errorf("no embedded up migrations; the embed pattern is wrong")
	}
	return ups[len(ups)-1].Version, nil
}

// migrationByVersion returns one embedded migration.
func migrationByVersion(version int) (migrationSource, bool, error) {
	ups, err := upMigrations()
	if err != nil {
		return migrationSource{}, false, err
	}
	for _, m := range ups {
		if m.Version == version {
			return m, true, nil
		}
	}
	return migrationSource{}, false, nil
}

// Verdict is what the objects on disk say about one migration.
type Verdict int

const (
	// VerdictUnknown means the migration creates nothing we can look for, so
	// its state cannot be settled either way. Data-only migrations land here.
	VerdictUnknown Verdict = iota
	// VerdictApplied means every object the migration creates is present.
	VerdictApplied
	// VerdictNotApplied means at least one is missing, which given atomicity
	// means none of the file ran.
	VerdictNotApplied
)

func (v Verdict) String() string {
	switch v {
	case VerdictApplied:
		return "applied"
	case VerdictNotApplied:
		return "not applied"
	default:
		return "cannot tell"
	}
}

// VersionReport is one migration's state, and why.
type VersionReport struct {
	Version int
	Name    string
	Verdict Verdict
	Checked int
	Missing []string
	Note    string
}

// RepairPlan is what the database says, what that means, and what to do.
type RepairPlan struct {
	// Recorded is the version in schema_migrations, or NoVersion when the
	// table is empty.
	Recorded int
	Dirty    bool
	// Head is the highest migration this build ships.
	Head int
	// Reports covers the versions walked, highest first.
	Reports []VersionReport
	// Target is the version to write. Equal to Recorded when nothing is wrong.
	Target int
	// Confident is true when the walk ended on solid ground: either nothing
	// needs changing, or it stopped at a version whose objects are all present.
	Confident bool
	Summary   string
}

// NoVersion marks an empty schema_migrations, matching golang-migrate's nil
// version.
const NoVersion = -1

// NeedsRepair reports whether the plan would change anything.
func (p RepairPlan) NeedsRepair() bool { return p.Target != p.Recorded }

// AnalyseRepair works out whether the recorded version is telling the truth,
// and what to write if it is not. It only reads.
func AnalyseRepair(ctx context.Context, pool *pgxpool.Pool) (RepairPlan, error) {
	head, err := headVersion()
	if err != nil {
		return RepairPlan{}, err
	}

	recorded, dirty, err := readRecordedVersion(ctx, pool)
	if err != nil {
		return RepairPlan{}, err
	}

	plan := RepairPlan{Recorded: recorded, Dirty: dirty, Head: head, Target: recorded}

	switch {
	case recorded == NoVersion:
		plan.Confident = true
		plan.Summary = "schema_migrations is empty, so this database has not been migrated yet. Nothing to repair."
		return plan, nil

	case recorded > head:
		plan.Summary = fmt.Sprintf(
			"schema_migrations is at version %d and this build ships %d. The database was migrated by a newer "+
				"build than this one, so there is nothing to repair here: run the newer image instead.",
			recorded, head)
		return plan, nil
	}

	target := recorded
	stopped := false
	for version := recorded; version >= 1; version-- {
		report, err := evaluateVersion(ctx, pool, version)
		if err != nil {
			return RepairPlan{}, err
		}

		if version == recorded && dirty {
			report.Verdict = VerdictNotApplied
			report.Note = "left dirty, so it was interrupted and rolled back"
		}

		plan.Reports = append(plan.Reports, report)

		if report.Verdict == VerdictNotApplied {
			target = version - 1
			continue
		}

		stopped = true
		plan.Confident = report.Verdict == VerdictApplied
		break
	}

	if !stopped {
		// Walked past migration 1 without finding anything applied, so the
		// right answer is to start over from an empty version.
		target = NoVersion
		plan.Confident = true
	}
	if target < 1 {
		target = NoVersion
	}
	plan.Target = target

	switch {
	case !plan.NeedsRepair():
		plan.Confident = true
		plan.Summary = fmt.Sprintf("schema_migrations is at version %d and the objects that migration creates are present. Nothing to repair.", recorded)
	case plan.Confident:
		plan.Summary = fmt.Sprintf(
			"schema_migrations claims version %d, but %s. Setting the version to %s lets those migrations run.",
			recorded, describeGap(plan), versionLabel(target))
	default:
		plan.Summary = fmt.Sprintf(
			"schema_migrations claims version %d and the migrations above %s did not run, but %s creates nothing this can look for, "+
				"so whether it ran cannot be settled from the schema. Restore the backup taken before the upgrade rather than guessing.",
			recorded, versionLabel(target), versionLabel(target))
	}

	return plan, nil
}

// describeGap summarises the versions the walk proved had not run.
func describeGap(p RepairPlan) string {
	var unapplied []string
	for _, r := range p.Reports {
		if r.Verdict == VerdictNotApplied {
			unapplied = append(unapplied, strconv.Itoa(r.Version))
		}
	}
	sort.Strings(unapplied)
	if len(unapplied) == 1 {
		return "migration " + unapplied[0] + " never ran"
	}
	return "migrations " + strings.Join(unapplied, ", ") + " never ran"
}

func versionLabel(v int) string {
	if v == NoVersion {
		return "empty"
	}
	return strconv.Itoa(v)
}

// evaluateVersion asks the database whether one migration's objects are there.
func evaluateVersion(ctx context.Context, pool *pgxpool.Pool, version int) (VersionReport, error) {
	source, ok, err := migrationByVersion(version)
	if err != nil {
		return VersionReport{}, err
	}
	report := VersionReport{Version: version}
	if !ok {
		report.Note = "not in this build"
		return report, nil
	}
	report.Name = source.Name

	ps := probes(source.SQL)
	report.Checked = len(ps)
	if len(ps) == 0 {
		report.Note = "creates nothing to look for"
		return report, nil
	}

	for _, p := range ps {
		present, err := probeExists(ctx, pool, p)
		if err != nil {
			return VersionReport{}, err
		}
		if !present {
			report.Missing = append(report.Missing, p.String())
		}
	}

	if len(report.Missing) > 0 {
		report.Verdict = VerdictNotApplied
	} else {
		report.Verdict = VerdictApplied
	}
	return report, nil
}

func probeExists(ctx context.Context, pool *pgxpool.Pool, p probe) (bool, error) {
	var exists bool
	var err error

	switch p.kind {
	case probeColumn:
		err = pool.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM information_schema.columns
			   WHERE table_schema = current_schema()
			     AND table_name = $1 AND column_name = $2)`, p.table, p.column).Scan(&exists)
	case probeFunction:
		err = pool.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM pg_proc p
			    JOIN pg_namespace n ON n.oid = p.pronamespace
			   WHERE n.nspname = current_schema() AND p.proname = $1)`, p.target).Scan(&exists)
	default:
		err = pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, p.target).Scan(&exists)
	}

	if err != nil {
		return false, fmt.Errorf("checking whether %s exists: %w", p, err)
	}
	return exists, nil
}

// readRecordedVersion reads schema_migrations without going through the
// migrator, so it works when the migrator refuses to start.
func readRecordedVersion(ctx context.Context, pool *pgxpool.Pool) (int, bool, error) {
	var present bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&present); err != nil {
		return 0, false, fmt.Errorf("looking for schema_migrations: %w", err)
	}
	if !present {
		return NoVersion, false, nil
	}

	var version int
	var dirty bool
	err := pool.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return NoVersion, false, nil
		}
		return 0, false, fmt.Errorf("reading schema_migrations: %w", err)
	}
	return version, dirty, nil
}

// ApplyRepair writes the version the plan settled on. It refuses a plan that
// would be a guess.
func ApplyRepair(databaseURL string, plan RepairPlan) error {
	if !plan.NeedsRepair() {
		return nil
	}
	if !plan.Confident {
		return fmt.Errorf("refusing to repair: %s", plan.Summary)
	}
	return forceVersion(databaseURL, plan.Target)
}

// Report renders a plan for a person reading a terminal.
func (p RepairPlan) Report(w io.Writer) {
	_, _ = fmt.Fprintf(w, "schema_migrations: version %s, dirty %t\n", versionLabel(p.Recorded), p.Dirty)
	_, _ = fmt.Fprintf(w, "this build ships migrations up to %d\n\n", p.Head)

	if len(p.Reports) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "VERSION\tMIGRATION\tSTATE\tEVIDENCE")
		for _, r := range p.Reports {
			evidence := r.Note
			if evidence == "" {
				switch {
				case len(r.Missing) > 0:
					evidence = fmt.Sprintf("%d of %d objects missing, including %s",
						len(r.Missing), r.Checked, r.Missing[0])
				case r.Checked > 0:
					evidence = fmt.Sprintf("all %d objects present", r.Checked)
				}
			}
			_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", r.Version, r.Name, r.Verdict, evidence)
		}
		_ = tw.Flush()
		_, _ = fmt.Fprintln(w)
	}

	_, _ = fmt.Fprintln(w, p.Summary)
	if p.NeedsRepair() && p.Confident {
		_, _ = fmt.Fprintf(w, "\nAction: set schema_migrations to version %s. Migrations %d onwards then run on the next start.\n",
			versionLabel(p.Target), p.Target+1)
	}
}

// forceVersion writes a version into schema_migrations and clears the dirty
// flag, which is the one thing this package does that a person would otherwise
// do with an UPDATE.
func forceVersion(databaseURL string, version int) error {
	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("creating migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, toPgx5URL(databaseURL))
	if err != nil {
		return fmt.Errorf("creating migrator: %w", err)
	}
	defer func() {
		if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
			slog.Error("closing migrator", "source_error", srcErr, "db_error", dbErr)
		}
	}()

	if err := m.Force(version); err != nil {
		return fmt.Errorf("setting schema_migrations to %s: %w", versionLabel(version), err)
	}
	return nil
}
