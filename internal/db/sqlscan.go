// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package db

import (
	"regexp"
	"strings"
)

// This file reads migration SQL as text rather than executing it, to answer two
// questions that the repair paths depend on:
//
//   - Is this file atomic? golang-migrate's pgx driver sends a migration to the
//     server as one simple query with no parameters, and Postgres wraps a
//     multi-statement simple query in an implicit transaction. So a migration
//     that fails rolls back in full, and rewinding schema_migrations so it runs
//     again is safe. That holds only while nothing in the file breaks out of
//     the transaction, which is what atomicityBlockers checks.
//
//   - What does this file create? If an object a migration creates is missing,
//     the migration did not finish, whatever schema_migrations claims.
//
// Both matter only for text outside comments, string literals and PL/pgSQL
// bodies, so everything here runs on the output of stripNoise.

// stripNoise blanks out comments, string literals and dollar-quoted bodies,
// keeping newlines so reported positions still make sense. Without it a BEGIN
// inside a PL/pgSQL block reads as transaction control and every migration with
// a DO block looks non-atomic.
func stripNoise(sql string) string {
	var b strings.Builder
	b.Grow(len(sql))

	keepNewlines := func(s string) {
		for _, r := range s {
			if r == '\n' {
				b.WriteByte('\n')
			} else {
				b.WriteByte(' ')
			}
		}
	}

	for i := 0; i < len(sql); {
		rest := sql[i:]

		switch {
		case strings.HasPrefix(rest, "--"):
			end := strings.IndexByte(rest, '\n')
			if end < 0 {
				end = len(rest)
			}
			keepNewlines(rest[:end])
			i += end

		case strings.HasPrefix(rest, "/*"):
			// Postgres block comments nest, so count depth rather than
			// stopping at the first close.
			depth, j := 1, 2
			for j < len(rest) && depth > 0 {
				switch {
				case strings.HasPrefix(rest[j:], "/*"):
					depth++
					j += 2
				case strings.HasPrefix(rest[j:], "*/"):
					depth--
					j += 2
				default:
					j++
				}
			}
			keepNewlines(rest[:j])
			i += j

		case rest[0] == '\'':
			j := 1
			for j < len(rest) {
				if rest[j] == '\'' {
					if j+1 < len(rest) && rest[j+1] == '\'' {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			keepNewlines(rest[:j])
			i += j

		case rest[0] == '"':
			// Quoted identifiers are names, not noise: keep them so the object
			// extraction below can see them.
			j := 1
			for j < len(rest) && rest[j] != '"' {
				j++
			}
			if j < len(rest) {
				j++
			}
			b.WriteString(rest[:j])
			i += j

		default:
			if tag := dollarTag(rest); tag != "" {
				end := strings.Index(rest[len(tag):], tag)
				if end < 0 {
					// Unterminated, which the server would reject anyway.
					keepNewlines(rest)
					i = len(sql)
					continue
				}
				stop := len(tag) + end + len(tag)
				keepNewlines(rest[:stop])
				i += stop
				continue
			}
			b.WriteByte(rest[0])
			i++
		}
	}

	return b.String()
}

var dollarTagPattern = regexp.MustCompile(`^\$([A-Za-z_][A-Za-z0-9_]*)?\$`)

// dollarTag returns the dollar-quote delimiter starting s, or "" if s does not
// start one. A bind placeholder like $1 is not a delimiter.
func dollarTag(s string) string {
	return dollarTagPattern.FindString(s)
}

// blocker is one reason a migration cannot be trusted to roll back in full.
type blocker struct {
	pattern *regexp.Regexp
	reason  string
}

var blockers = []blocker{
	{regexp.MustCompile(`(?i)\bCONCURRENTLY\b`),
		"CREATE/DROP INDEX CONCURRENTLY cannot run inside a transaction"},
	{regexp.MustCompile(`(?i)\bVACUUM\b`),
		"VACUUM cannot run inside a transaction"},
	{regexp.MustCompile(`(?i)\bREINDEX\b`),
		"REINDEX may run outside a transaction"},
	{regexp.MustCompile(`(?i)\bALTER\s+SYSTEM\b`),
		"ALTER SYSTEM cannot run inside a transaction"},
	{regexp.MustCompile(`(?i)\b(CREATE|DROP)\s+DATABASE\b`),
		"CREATE/DROP DATABASE cannot run inside a transaction"},
	{regexp.MustCompile(`(?i)\b(CREATE|DROP)\s+TABLESPACE\b`),
		"CREATE/DROP TABLESPACE cannot run inside a transaction"},
	{regexp.MustCompile(`(?i)(\A|;)\s*(BEGIN|COMMIT|ROLLBACK|START\s+TRANSACTION|SAVEPOINT)\b`),
		"the file manages transactions itself, so a failure may leave part of it applied"},
}

// atomicityBlockers reports why a migration might not roll back cleanly. An
// empty result means a failure leaves the schema exactly as it was, which is
// the precondition for rewinding the version so the migration runs again.
func atomicityBlockers(sql string) []string {
	clean := stripNoise(sql)

	var reasons []string
	for _, b := range blockers {
		if b.pattern.MatchString(clean) {
			reasons = append(reasons, b.reason)
		}
	}
	return reasons
}

// probeKind says how to ask the server whether an object exists.
type probeKind int

const (
	probeRelation probeKind = iota // table, view, index or sequence
	probeColumn
	probeFunction
)

// probe is one object a migration creates, and so one piece of evidence about
// whether that migration ran. Absence is the reliable direction: because a
// migration is atomic, one missing object proves the whole file did not apply.
// Presence is weaker, since IF NOT EXISTS and OR REPLACE mean an object may
// predate the migration that mentions it.
type probe struct {
	kind   probeKind
	target string // relation name, table.column, or function name
	table  string // set for probeColumn
	column string // set for probeColumn
}

func (p probe) String() string {
	switch p.kind {
	case probeColumn:
		return "column " + p.table + "." + p.column
	case probeFunction:
		return "function " + p.target
	default:
		return p.target
	}
}

var (
	createRelation = regexp.MustCompile(
		`(?is)\bCREATE\s+(?:OR\s+REPLACE\s+)?(?:UNIQUE\s+)?(?:MATERIALIZED\s+)?` +
			`(?:TABLE|VIEW|INDEX|SEQUENCE)\s+(?:IF\s+NOT\s+EXISTS\s+)?("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)`)
	createTemp = regexp.MustCompile(`(?is)\bCREATE\s+(?:GLOBAL\s+|LOCAL\s+)?(?:TEMP|TEMPORARY|UNLOGGED)\s`)
	addColumn  = regexp.MustCompile(
		`(?is)\bALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)\s+` +
			`ADD\s+COLUMN\s+(?:IF\s+NOT\s+EXISTS\s+)?("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)`)
	createFunction = regexp.MustCompile(
		`(?is)\bCREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\s+("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)`)
)

// identifier normalises a captured name the way Postgres would: quoted names
// keep their case, bare ones fold to lower.
func identifier(tok string) string {
	if len(tok) >= 2 && tok[0] == '"' {
		return tok[1 : len(tok)-1]
	}
	return strings.ToLower(tok)
}

// probes lists the objects a migration creates. Temporary and unlogged
// relations are skipped: they say nothing about a migration that has finished.
func probes(sql string) []probe {
	clean := stripNoise(sql)

	seen := map[string]bool{}
	var out []probe
	add := func(p probe) {
		if key := p.String(); !seen[key] {
			seen[key] = true
			out = append(out, p)
		}
	}

	for _, m := range createRelation.FindAllStringSubmatchIndex(clean, -1) {
		if createTemp.MatchString(clean[m[0]:m[1]]) {
			continue
		}
		add(probe{kind: probeRelation, target: identifier(clean[m[2]:m[3]])})
	}
	for _, m := range addColumn.FindAllStringSubmatch(clean, -1) {
		add(probe{kind: probeColumn, table: identifier(m[1]), column: identifier(m[2])})
	}
	for _, m := range createFunction.FindAllStringSubmatch(clean, -1) {
		add(probe{kind: probeFunction, target: identifier(m[1])})
	}

	return out
}
