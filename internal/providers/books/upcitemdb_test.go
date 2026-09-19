// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package books

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The two bodies are trimmed real answers from 2026-09-18 for barcodes
// scanned from the collection.
const (
	upcitemdbEndersGame = `{"code":"OK","total":1,"items":[{"title":"Ender's Game by Orson Scott Card. Author's Definitive Edition. VGT 1994","description":"","brand":"Doherty Associates, LLC, Tom&Co","category":"Media > Books","images":["https://example.test/enders.jpg"]}]}`
	upcitemdbStargate   = `{"code":"OK","total":1,"items":[{"title":"Stargate Universe : Back to Destiny","brand":"","publisher":"Mark L Haynes; J C Vaughn; Giancarlo Caracuzzo","isbn":"9781945205125","category":"Media > Books","images":[]}]}`
	upcitemdbNotFound   = `{"code":"OK","total":0,"items":[]}`
)

func upcitemdbServer(t *testing.T, body string, gotPath, gotUPC, gotKey *string) *UPCitemdbProvider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotPath, *gotUPC, *gotKey = r.URL.Path, r.URL.Query().Get("upc"), r.Header.Get("user_key")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	p := NewUPCitemdbProvider()
	p.baseURL = srv.URL
	return p
}

func TestUPCitemdb_PaperbackWithoutISBN(t *testing.T) {
	var path, upc, key string
	p := upcitemdbServer(t, upcitemdbEndersGame, &path, &upc, &key)

	r, err := p.LookupByUPC(context.Background(), "037145006994")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/prod/trial/lookup" || key != "" {
		t.Errorf("without a key it should use the free endpoint; got %s, key %q", path, key)
	}
	if r.Title != "Ender's Game" {
		t.Errorf("title = %q", r.Title)
	}
	if len(r.Authors) != 1 || r.Authors[0] != "Orson Scott Card" {
		t.Errorf("authors = %v", r.Authors)
	}
	if r.Publisher != "Doherty Associates, LLC, Tom&Co" || r.ISBN13 != "" || r.CoverURL == "" {
		t.Errorf("publisher %q, isbn %q, cover %q", r.Publisher, r.ISBN13, r.CoverURL)
	}
}

// The comic had its creators in UPCitemdb's publisher field; that must not
// become the publisher, and the ISBN must come through for the ISBN path.
func TestUPCitemdb_KeepsISBNAndIgnoresPublisherField(t *testing.T) {
	var path, upc, key string
	p := upcitemdbServer(t, upcitemdbStargate, &path, &upc, &key)

	r, err := p.LookupByUPC(context.Background(), "9781945205125")
	if err != nil {
		t.Fatal(err)
	}
	if r.ISBN13 != "9781945205125" {
		t.Errorf("isbn = %q", r.ISBN13)
	}
	if r.Publisher != "" {
		t.Errorf("publisher = %q, want empty", r.Publisher)
	}
	if r.Title != "Stargate Universe : Back to Destiny" || r.Authors != nil {
		t.Errorf("title %q authors %v", r.Title, r.Authors)
	}
}

func TestUPCitemdb_StripsAddOnAndUsesKey(t *testing.T) {
	var path, upc, key string
	p := upcitemdbServer(t, upcitemdbNotFound, &path, &upc, &key)
	p.Configure(map[string]string{"api_key": "k"})

	r, err := p.LookupByUPC(context.Background(), "75960608790700111")
	if err != nil {
		t.Fatal(err)
	}
	if r != nil {
		t.Errorf("no items should be no result, got %+v", r)
	}
	if upc != "759606087907" {
		t.Errorf("sent %q, want the add-on stripped", upc)
	}
	if path != "/prod/v1/lookup" || key != "k" {
		t.Errorf("with a key: path %s, key %q", path, key)
	}
}

func TestUPCitemdb_EnabledByDefault(t *testing.T) {
	p := NewUPCitemdbProvider()
	p.Configure(map[string]string{})
	if !p.Enabled() {
		t.Error("should be on with no config: it needs no key")
	}
	p.Configure(map[string]string{"enabled": "false"})
	if p.Enabled() {
		t.Error("admin turned it off")
	}
}
