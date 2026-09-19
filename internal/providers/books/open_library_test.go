// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package books

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Open Library's Books API began answering 404 for every ISBN around
// 2026-09-18, which turned every lookup into an error. The edition JSON still
// works, and is enough on its own.
func TestOpenLibrary_BooksAPIDownUsesEdition(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/books", http.NotFound)
	mux.HandleFunc("/isbn/9780441172719.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"title":"Dune","authors":[{"key":"/authors/OL79034A"}],"publishers":["Ace Books"],
			"publish_date":"1987","number_of_pages":535,"covers":[15166231],"works":[{"key":"/works/OL893414W"}],
			"languages":[{"key":"/languages/eng"}],"isbn_10":["0441172717"]}`))
	})
	mux.HandleFunc("/authors/OL79034A.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"Frank Herbert"}`))
	})
	mux.HandleFunc("/works/OL893414W.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"description":"Set on the desert planet Arrakis."}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := NewOpenLibraryProvider()
	p.baseURL = srv.URL
	r, err := p.LookupByISBN(context.Background(), "9780441172719")
	if err != nil {
		t.Fatalf("a Books API 404 must not fail the lookup: %v", err)
	}
	if r == nil {
		t.Fatal("no result")
	}
	if r.Title != "Dune" || len(r.Authors) != 1 || r.Authors[0] != "Frank Herbert" {
		t.Errorf("title %q authors %v", r.Title, r.Authors)
	}
	if r.Publisher != "Ace Books" || r.PageCount == nil || *r.PageCount != 535 {
		t.Errorf("publisher %q pages %v", r.Publisher, r.PageCount)
	}
	if r.ISBN13 != "9780441172719" || r.ISBN10 != "0441172717" {
		t.Errorf("isbns %q %q", r.ISBN13, r.ISBN10)
	}
	if r.CoverURL != "https://covers.openlibrary.org/b/id/15166231-M.jpg" {
		t.Errorf("cover %q", r.CoverURL)
	}
	if r.Description != "Set on the desert planet Arrakis." || r.Language == "" {
		t.Errorf("description %q language %q", r.Description, r.Language)
	}
}

// Neither endpoint knows the book: no record, not an error.
func TestOpenLibrary_UnknownBookIsNoRecord(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	p := NewOpenLibraryProvider()
	p.baseURL = srv.URL
	r, err := p.LookupByISBN(context.Background(), "9798058688639")
	if err != nil || r != nil {
		t.Errorf("want no record, got %+v, %v", r, err)
	}
}
