// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package books

import (
	"net/http"
	"testing"
)

// TestProviderClientsAreBounded pins the BookSearchProvider contract: every
// provider must bound its own HTTP calls.
//
// Registry.SearchBooks starts its deadline only after some provider has
// already responded, so before the first result the sole backstops are a
// provider returning and the request context being cancelled. A provider
// built on a zero-value http.Client has no timeout at all, and one hung
// connection then stalls every search that includes it for as long as the
// caller is willing to wait.
//
// ISBNdb doesn't implement SearchBooks today, so it isn't on that path yet.
// It's covered here anyway — the cost of a timeout is nothing, and the cost
// of remembering this rule when adding SearchBooks later is where bugs live.
func TestProviderClientsAreBounded(t *testing.T) {
	cases := []struct {
		name   string
		client *http.Client
	}{
		{"google_books", NewGoogleBooksProvider().client},
		{"hardcover", NewHardcoverProvider().client},
		{"isbndb", NewISBNdbProvider().client},
		{"open_library", NewOpenLibraryProvider().client},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.client == nil {
				t.Fatal("provider has a nil http.Client")
			}
			if tc.client.Timeout <= 0 {
				t.Errorf("http.Client has no timeout; an unbounded provider stalls every search it takes part in")
			}
		})
	}
}
