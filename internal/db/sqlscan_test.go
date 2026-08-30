// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package db

import (
	"strings"
	"testing"
)

func TestStripNoiseKeepsCodeAndDropsTheRest(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		wants   []string
		unwants []string
	}{
		{
			name:    "line comment",
			sql:     "CREATE TABLE a (); -- CREATE TABLE b ()\nCREATE TABLE c ();",
			wants:   []string{"TABLE a", "TABLE c"},
			unwants: []string{"TABLE b"},
		},
		{
			name:    "nested block comment",
			sql:     "/* outer /* inner CREATE TABLE b */ still outer */ CREATE TABLE a ();",
			wants:   []string{"TABLE a"},
			unwants: []string{"TABLE b"},
		},
		{
			name:    "string literal",
			sql:     "INSERT INTO t VALUES ('BEGIN; VACUUM;'); CREATE TABLE a ();",
			unwants: []string{"VACUUM"},
			wants:   []string{"TABLE a"},
		},
		{
			name:    "escaped quote inside a literal",
			sql:     "INSERT INTO t VALUES ('it''s VACUUM here'); CREATE TABLE a ();",
			unwants: []string{"VACUUM"},
			wants:   []string{"TABLE a"},
		},
		{
			name: "plpgsql body",
			sql: "DO $$ BEGIN IF true THEN RAISE EXCEPTION 'no'; END IF; END $$;\n" +
				"CREATE TABLE a ();",
			unwants: []string{"RAISE"},
			wants:   []string{"TABLE a"},
		},
		{
			name:  "bind placeholder is not a dollar quote",
			sql:   "SELECT $1; CREATE TABLE a ();",
			wants: []string{"TABLE a"},
		},
		{
			name:  "quoted identifiers survive",
			sql:   `CREATE TABLE "MixedCase" ();`,
			wants: []string{`"MixedCase"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripNoise(tt.sql)
			for _, want := range tt.wants {
				if !strings.Contains(got, want) {
					t.Errorf("stripNoise dropped %q\ngot: %s", want, got)
				}
			}
			for _, unwanted := range tt.unwants {
				if strings.Contains(got, unwanted) {
					t.Errorf("stripNoise kept %q\ngot: %s", unwanted, got)
				}
			}
		})
	}
}

func TestAtomicityBlockers(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		blocked bool
	}{
		{"plain ddl", "CREATE TABLE a (id int);", false},
		{"do block with begin", "DO $$ BEGIN PERFORM 1; END $$;", false},
		{"begin in a comment", "-- BEGIN;\nCREATE TABLE a ();", false},
		{"concurrent index", "CREATE INDEX CONCURRENTLY a_idx ON a (id);", true},
		{"vacuum", "VACUUM ANALYZE a;", true},
		{"explicit transaction", "BEGIN;\nCREATE TABLE a ();\nCOMMIT;", true},
		{"alter system", "ALTER SYSTEM SET work_mem = '1GB';", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := atomicityBlockers(tt.sql)
			if blocked := len(got) > 0; blocked != tt.blocked {
				t.Errorf("atomicityBlockers = %v, want blocked = %t", got, tt.blocked)
			}
		})
	}
}

func TestProbes(t *testing.T) {
	sql := `
		CREATE TABLE copies (id uuid);
		CREATE INDEX IF NOT EXISTS copies_idx ON copies (id);
		CREATE OR REPLACE VIEW held_books AS SELECT 1;
		ALTER TABLE books ADD COLUMN IF NOT EXISTS sort_title text;
		CREATE OR REPLACE FUNCTION sort_title(t text) RETURNS text AS $$ BEGIN RETURN t; END $$ LANGUAGE plpgsql;
		CREATE TEMP TABLE scratch (id int);
		DO $$ BEGIN CREATE TABLE never_seen (); END $$;
	`

	var got []string
	for _, p := range probes(sql) {
		got = append(got, p.String())
	}

	want := []string{"copies", "copies_idx", "held_books", "column books.sort_title", "function sort_title"}
	if len(got) != len(want) {
		t.Fatalf("probes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("probe %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestEveryMigrationIsAtomic guards the assumption the whole repair path rests
// on. golang-migrate sends a migration as one simple query, which Postgres runs
// in an implicit transaction, so a failure changes nothing and the file can be
// retried. Add a statement that breaks out of that transaction and rewinding
// stops being safe, so this fails rather than letting it ship.
func TestEveryMigrationIsAtomic(t *testing.T) {
	ups, err := upMigrations()
	if err != nil {
		t.Fatalf("reading migrations: %v", err)
	}
	if len(ups) == 0 {
		t.Fatal("no migrations found")
	}

	for _, source := range ups {
		if reasons := atomicityBlockers(source.SQL); len(reasons) > 0 {
			t.Errorf("%06d_%s.up.sql cannot roll back cleanly: %s",
				source.Version, source.Name, strings.Join(reasons, "; "))
		}
	}
}

// TestMigrationProbeCoverage reports which migrations create nothing that can
// be looked for afterwards. Those are data-only migrations and are fine, but
// repair has to stop when it reaches one, so the list is worth seeing rather
// than growing quietly.
func TestMigrationProbeCoverage(t *testing.T) {
	ups, err := upMigrations()
	if err != nil {
		t.Fatalf("reading migrations: %v", err)
	}

	var blind []string
	for _, source := range ups {
		if len(probes(source.SQL)) == 0 {
			blind = append(blind, source.Name)
		}
	}
	t.Logf("%d of %d migrations create nothing repair can probe: %s",
		len(blind), len(ups), strings.Join(blind, ", "))

	if len(blind) == len(ups) {
		t.Error("no migration creates anything probeable, which means the extraction is broken")
	}
}
