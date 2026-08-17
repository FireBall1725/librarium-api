// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// modulePrefix is what swag calls this module's packages internally: the import
// path with every separator flattened to an underscore.
const modulePrefix = "github_com_fireball1725_librarium-api_"

// annotationLine matches the swag directives that name a type. @Description is
// excluded on purpose — it is prose, and "e.g. Foo.Bar" in a sentence is not a
// type reference.
var annotationLine = regexp.MustCompile(`^\s*//\s*@(Success|Failure|Param)\b`)

// typeRef finds `alias.Type` pairs. The alias may carry the hyphen and
// underscores of a mangled module path, which is why it is not just \w+.
var typeRef = regexp.MustCompile(`\b([a-z][a-zA-Z0-9_\-]*)\.([A-Z][A-Za-z0-9_]*)\b`)

// importLine pulls the path out of an import, with or without an explicit name.
var importLine = regexp.MustCompile(`^\s*(?:([\w.]+)\s+)?"([^"]+)"\s*$`)

// TestAnnotationTypesResolve catches the mistake that broke `make docs`.
//
// swag resolves a short package alias in a comment against the imports of the
// file that comment sits in. A handler file has no reason to import a package
// it only names in an annotation, so `responses.BookResponse` failed to resolve
// in the nine files that wrote it — and because one bad annotation aborts the
// whole `swag init` run, every route stopped regenerating. The committed spec
// sat thirteen routes behind before anyone noticed, and a TODO in sync.go blamed
// a swag parser bug that did not exist.
//
// This fails in `go test`, where it is seen, rather than in a docs target that
// is easy to skip. Either import the package or use the mangled full path:
//
//	@Success 200 {object} github_com_fireball1725_librarium-api_internal_api_responses.BookResponse
func TestAnnotationTypesResolve(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing handlers: %v", err)
	}
	if len(files) < 5 {
		t.Fatalf("found %d files in the handlers package; the glob is wrong, and a "+
			"glob that finds nothing would make this test pass by checking nothing", len(files))
	}

	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		known := importedNames(string(src))
		known["handlers"] = true // the package this file is in

		for i, line := range strings.Split(string(src), "\n") {
			if !annotationLine.MatchString(line) {
				continue
			}
			for _, m := range typeRef.FindAllStringSubmatch(line, -1) {
				alias, typ := m[1], m[2]
				checked++
				if known[alias] || strings.HasPrefix(alias, modulePrefix) {
					continue
				}
				t.Errorf("%s:%d: annotation names %s.%s, but %s is neither the "+
					"current package nor imported by this file, so swag cannot resolve it.\n"+
					"    Use %s<path>.%s, or import the package.",
					f, i+1, alias, typ, alias, modulePrefix, typ)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no annotation type references found; the regexes no longer match the " +
			"comment style, so this test is passing without checking anything")
	}
	t.Logf("checked %d annotation type references across %d files", checked, len(files))
}

// importedNames returns the local names a file's imports bind, keyed for lookup.
func importedNames(src string) map[string]bool {
	out := map[string]bool{}
	inBlock := false
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "import (":
			inBlock = true
			continue
		case inBlock && trimmed == ")":
			inBlock = false
			continue
		case strings.HasPrefix(trimmed, "import "):
			line = strings.TrimPrefix(trimmed, "import ")
		case !inBlock:
			continue
		}
		m := importLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if m[1] != "" {
			out[m[1]] = true // explicit alias
			continue
		}
		parts := strings.Split(m[2], "/")
		out[parts[len(parts)-1]] = true
	}
	return out
}
