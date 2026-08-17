// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package licences

import (
	"bufio"
	"os"
	"runtime/debug"
	"strings"
	"testing"
)

// TestEveryRequirementHasALicence is the reason the module list is derived
// rather than written down.
//
// A hand-kept list fails silently: you add a dependency, forget the row, and
// the licences page keeps rendering, just missing one. Here the module arrives
// on its own, so the only thing that can be missing is its SPDX identifier,
// and that is what this fails on.
//
// It reads go.mod, not debug.ReadBuildInfo. A test binary reports zero
// dependencies from build info — the call succeeds and Deps is empty — so the
// obvious version of this test passes by checking nothing. go.mod is a superset
// of the linked set, which is the safe direction to be wrong in.
func TestEveryRequirementHasALicence(t *testing.T) {
	mods, err := requirementsFromGoMod("../../go.mod")
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	if len(mods) < 10 {
		t.Fatalf("parsed only %d requirements from go.mod; the parser is wrong, "+
			"and a parser that finds nothing would make this test pass on an empty map", len(mods))
	}
	for _, m := range mods {
		if spdxByModule[m] == "" {
			t.Errorf("module %s has no SPDX identifier in spdxByModule.\n"+
				"Read its LICENSE under $(go env GOMODCACHE) and add a row.", m)
		}
	}
}

// TestComponentsFromSortsAndResolves covers the shape clients depend on:
// case-insensitive ordering, a resolved licence where one is known, and an
// empty one where it is not — dropped rows would hide exactly the component
// worth showing.
func TestComponentsFromSortsAndResolves(t *testing.T) {
	got := componentsFrom([]*debug.Module{
		{Path: "github.com/zzz/last", Version: "v1.0.0"},
		{Path: "github.com/google/uuid", Version: "v1.6.0"},
		{Path: "github.com/Aaa/upper", Version: "v0.1.0"},
	})

	names := make([]string, len(got))
	for i, c := range got {
		names[i] = c.Name
	}
	want := []string{"github.com/Aaa/upper", "github.com/google/uuid", "github.com/zzz/last"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("sorted %v, want %v", names, want)
	}

	byName := map[string]Component{}
	for _, c := range got {
		byName[c.Name] = c
	}
	if l := byName["github.com/google/uuid"].Licence; l != "BSD-3-Clause" {
		t.Errorf("uuid licence = %q, want BSD-3-Clause", l)
	}
	if l := byName["github.com/zzz/last"].Licence; l != "" {
		t.Errorf("unknown module licence = %q, want empty", l)
	}
	if v := byName["github.com/google/uuid"].Version; v != "v1.6.0" {
		t.Errorf("version = %q, want v1.6.0", v)
	}
}

// TestComponentsFromPrefersTheReplacement checks that a replaced module reports
// what is actually compiled in, since that is what carries the notice.
func TestComponentsFromPrefersTheReplacement(t *testing.T) {
	got := componentsFrom([]*debug.Module{{
		Path:    "github.com/original/thing",
		Version: "v1.0.0",
		Replace: &debug.Module{Path: "github.com/google/uuid", Version: "v1.6.0"},
	}})
	if len(got) != 1 {
		t.Fatalf("got %d components, want 1", len(got))
	}
	if got[0].Name != "github.com/google/uuid" || got[0].Version != "v1.6.0" {
		t.Errorf("got %s@%s, want the replacement", got[0].Name, got[0].Version)
	}
	if got[0].Licence != "BSD-3-Clause" {
		t.Errorf("licence = %q, want the replacement's", got[0].Licence)
	}
}

// requirementsFromGoMod pulls every module path out of go.mod's require
// directives, both the block and single-line forms. Indirect requirements count
// too: they are linked into the binary and shipped like anything else.
//
// A hand-rolled scanner rather than golang.org/x/mod, which would mean taking a
// dependency in order to list dependencies.
func requirementsFromGoMod(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var mods []string
	inRequireBlock := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i]) // drops "// indirect" and comments
		}
		switch {
		case line == "":
		case inRequireBlock && line == ")":
			inRequireBlock = false
		case line == "require (":
			inRequireBlock = true
		case strings.HasPrefix(line, "require "):
			// Single-line form: require example.com/mod v1.2.3
			if fields := strings.Fields(line); len(fields) >= 3 {
				mods = append(mods, fields[1])
			}
		case inRequireBlock:
			// Only require blocks are scanned, so an exclude or replace block
			// cannot leak entries in here.
			if fields := strings.Fields(line); len(fields) >= 2 && strings.HasPrefix(fields[1], "v") {
				mods = append(mods, fields[0])
			}
		}
	}
	return mods, sc.Err()
}
