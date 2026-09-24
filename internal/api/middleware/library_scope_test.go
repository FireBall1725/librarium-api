// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package middleware

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestChecksForFollowsThePath(t *testing.T) {
	cases := []struct {
		pattern string
		want    []string // params checked, in order
	}{
		{"PUT /api/v1/libraries/{library_id}/series/{series_id}", []string{"series_id"}},
		{"DELETE /api/v1/libraries/{library_id}/series/{series_id}/arcs/{arc_id}", []string{"series_id", "arc_id"}},
		// Under a series the series check scopes the book; no held check.
		{"DELETE /api/v1/libraries/{library_id}/series/{series_id}/books/{book_id}", []string{"series_id"}},
		{"PUT /api/v1/libraries/{library_id}/books/{book_id}", []string{"book_id"}},
		{"PUT /api/v1/libraries/{library_id}/books/{book_id}/editions/{edition_id}", []string{"book_id", "edition_id"}},
		{"GET /api/v1/libraries/{library_id}/books/{book_id}/editions/{edition_id}/files/{file_id}", []string{"book_id", "edition_id", "file_id"}},
		// Reading a shared work stays open; adding one the library doesn't hold must too.
		{"GET /api/v1/libraries/{library_id}/books/{book_id}", nil},
		{"POST /api/v1/libraries/{library_id}/books/{book_id}", nil},
		{"GET /api/v1/libraries/{library_id}/books/{book_id}/editions/{edition_id}/my-interaction", []string{"edition_id"}},
		{"GET /api/v1/libraries/{library_id}/books", nil},
		{"GET /api/v1/books/{book_id}", nil},
	}
	for _, c := range cases {
		method, _, _ := strings.Cut(c.pattern, " ")
		var got []string
		for _, ch := range checksFor(method, c.pattern) {
			got = append(got, ch.param)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: checked %v, want %v", c.pattern, got, c.want)
		}
	}
}

// Every id in a library-scoped route is either checked here or scoped by its
// handler. A new route with a new kind of id fails this until someone decides
// which, which is how the last forty-nine slipped through.
func TestEveryLibraryRouteIDIsScoped(t *testing.T) {
	src, err := os.ReadFile("../router.go")
	if err != nil {
		t.Fatal(err)
	}
	// Handlers that scope these themselves, by passing library_id down.
	handlerScoped := map[string]bool{"user_id": true, "kiosk_id": true, "contributor_id": true, "import_id": true}
	route := regexp.MustCompile(`mux\.Handle\("([A-Z]+ /api/v1/libraries/\{library_id\}/[^"]+)",\s*requireLibraryPerm\(`)
	param := regexp.MustCompile(`\{([a-z_]+_id)\}`)
	n := 0
	for _, m := range route.FindAllStringSubmatch(string(src), -1) {
		pattern := m[1]
		method, _, _ := strings.Cut(pattern, " ")
		checked := map[string]bool{"library_id": true}
		for _, c := range checksFor(method, pattern) {
			checked[c.param] = true
		}
		for _, p := range param.FindAllStringSubmatch(pattern, -1) {
			name := p[1]
			if checked[name] || handlerScoped[name] {
				continue
			}
			// A book is readable through any library, and under a series or a
			// shelf its parent scopes it.
			if name == "book_id" && (method == "GET" || method == "POST" || !strings.Contains(pattern, "{library_id}/books/{book_id}")) {
				continue
			}
			t.Errorf("%s: {%s} is not checked against its library", pattern, name)
		}
		n++
	}
	if n < 50 {
		t.Fatalf("matched only %d library routes; has router.go changed shape?", n)
	}
}
