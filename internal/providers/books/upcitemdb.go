// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package books

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/fireball1725/librarium-api/internal/providers"
)

// UPCitemdbProvider looks up the UPC on a comic, manga volume or mass-market
// paperback. It's a retail product database, not a bibliographic one: it
// knows the title and often the publisher, and sometimes the ISBN. When it
// has the ISBN the client can finish through the ISBN providers; when it
// doesn't, the title seeds a search.
//
// The free plan needs no key and allows 100 lookups a day per IP, which fits
// a self-hoster scanning their own shelves. A key moves it to the paid API.
type UPCitemdbProvider struct {
	base
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewUPCitemdbProvider() *UPCitemdbProvider {
	return &UPCitemdbProvider{
		base:    base{enabled: true},
		baseURL: "https://api.upcitemdb.com",
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *UPCitemdbProvider) Info() providers.ProviderInfo {
	return providers.ProviderInfo{
		Name:         "upcitemdb",
		DisplayName:  "UPCitemdb",
		Description:  "Looks up the UPC barcode on comics, manga and mass-market paperbacks that have no ISBN barcode. Free for 100 lookups a day without a key.",
		RequiresKey:  false,
		Capabilities: []string{providers.CapBookUPC},
		HelpText:     "Works without a key. A paid plan's key raises the daily limit.",
		HelpURL:      "https://devs.upcitemdb.com/",
		ConfigFields: []providers.ConfigField{
			{
				Key:      "api_key",
				Label:    "API key",
				Type:     "password",
				HelpText: "Optional. Leave blank for the free plan.",
			},
		},
	}
}

func (p *UPCitemdbProvider) Configure(cfg map[string]string) {
	p.apiKey = strings.TrimSpace(cfg["api_key"])
	if v, ok := cfg["enabled"]; ok {
		p.enabled = v != "false"
	} else {
		p.enabled = true
	}
}

func (p *UPCitemdbProvider) LookupByUPC(ctx context.Context, code string) (*providers.BookResult, error) {
	// UPCitemdb rejects the add-on, and keys retail items on the base code.
	if len(code) == 17 || len(code) == 18 {
		code = code[:len(code)-5]
	}

	path := "/prod/trial/lookup"
	if p.apiKey != "" {
		path = "/prod/v1/lookup"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path+"?upc="+url.QueryEscape(code), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if p.apiKey != "" {
		req.Header.Set("user_key", p.apiKey)
		req.Header.Set("key_type", "3scale")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upcitemdb returned status %d", resp.StatusCode)
	}

	var body upcitemdbResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if len(body.Items) == 0 {
		return nil, nil
	}
	return upcitemdbResult(body.Items[0]), nil
}

func upcitemdbResult(it upcitemdbItem) *providers.BookResult {
	title, authors := splitListingTitle(it.Title)
	r := &providers.BookResult{
		Provider:        "upcitemdb",
		ProviderDisplay: "UPCitemdb",
		Title:           title,
		Authors:         authors,
		// brand is where the publisher shows up; its publisher field has held
		// a comic's creators instead.
		Publisher:   strings.TrimSpace(it.Brand),
		Description: strings.TrimSpace(it.Description),
	}
	if isbn := strings.ReplaceAll(it.ISBN, "-", ""); validISBN13(isbn) {
		r.ISBN13 = isbn
	}
	if len(it.Images) > 0 {
		r.CoverURL = it.Images[0]
	}
	if it.Category != "" {
		r.Categories = []string{it.Category}
	}
	return r
}

// listingBy matches a retail listing like "Ender's Game by Orson Scott Card.
// Author's Definitive Edition. VGT 1994": the title, then the author up to the
// first full stop.
var listingBy = regexp.MustCompile(`^(.+?)\s+by\s+([^.,;]+)`)

// splitListingTitle pulls the author out of a listing title when it's written
// as "<title> by <author>", and leaves anything else untouched. It's a guess,
// so it only fires on that one shape.
func splitListingTitle(raw string) (string, []string) {
	raw = strings.TrimSpace(raw)
	m := listingBy.FindStringSubmatch(raw)
	if m == nil {
		return raw, nil
	}
	return strings.TrimSpace(m[1]), []string{strings.TrimSpace(m[2])}
}

func validISBN13(s string) bool {
	if len(s) != 13 || (!strings.HasPrefix(s, "978") && !strings.HasPrefix(s, "979")) {
		return false
	}
	sum := 0
	for i, c := range s {
		if c < '0' || c > '9' {
			return false
		}
		d := int(c - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return sum%10 == 0
}

// ─── UPCitemdb API types ──────────────────────────────────────────────────────

type upcitemdbResponse struct {
	Code  string          `json:"code"`
	Items []upcitemdbItem `json:"items"`
}

type upcitemdbItem struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Brand       string   `json:"brand"`
	ISBN        string   `json:"isbn"`
	Category    string   `json:"category"`
	Images      []string `json:"images"`
}
