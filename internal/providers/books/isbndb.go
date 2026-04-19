// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package books

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fireball1725/librarium-api/internal/providers"
)

// ISBNdbProvider looks up books via the ISBNdb v2 API.
// Paid service: https://isbndb.com/apidocs/v2 (~$14.99/mo).
type ISBNdbProvider struct {
	base
	apiKey string
	client *http.Client
}

func NewISBNdbProvider() *ISBNdbProvider {
	return &ISBNdbProvider{
		base:   base{enabled: false},
		client: &http.Client{},
	}
}

func (p *ISBNdbProvider) Info() providers.ProviderInfo {
	return providers.ProviderInfo{
		Name:         "isbndb",
		DisplayName:  "ISBNdb",
		Description:  "Comprehensive ISBN database. Paid subscription required (~$14.99/month).",
		RequiresKey:  true,
		Capabilities: []string{providers.CapBookISBN},
		HelpText:     "Subscribe at ISBNdb.com, then copy your API key from the account dashboard.",
		HelpURL:      "https://isbndb.com/apidocs/v2",
	}
}

func (p *ISBNdbProvider) Configure(cfg map[string]string) {
	p.apiKey = cfg["api_key"]
	if v, ok := cfg["enabled"]; ok {
		p.enabled = v == "true" && p.apiKey != ""
	} else {
		p.enabled = p.apiKey != ""
	}
}

func (p *ISBNdbProvider) LookupByISBN(ctx context.Context, isbn string) (*providers.BookResult, error) {
	apiURL := fmt.Sprintf("https://api2.isbndb.com/book/%s", isbn)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("isbndb returned status %d", resp.StatusCode)
	}

	var body isbndbResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	b := body.Book
	result := &providers.BookResult{
		Provider:        "isbndb",
		ProviderDisplay: "ISBNdb",
		Title:           b.Title,
		Subtitle:        b.TitleLong,
		Authors:         b.Authors,
		Publisher:       b.Publisher,
		PublishDate:     b.DatePublished,
		Description:     b.Synopsis,
		Language:        b.Language,
		ISBN10:          b.ISBN,
		ISBN13:          b.ISBN13,
		CoverURL:        b.Image,
	}

	if b.Pages > 0 {
		n := b.Pages
		result.PageCount = &n
	}

	// If title_long is same as title, clear subtitle
	if result.Subtitle == result.Title {
		result.Subtitle = ""
	}

	return result, nil
}

// ─── ISBNdb API types ─────────────────────────────────────────────────────────

type isbndbResponse struct {
	Book isbndbBook `json:"book"`
}

type isbndbBook struct {
	Title         string   `json:"title"`
	TitleLong     string   `json:"title_long"`
	ISBN          string   `json:"isbn"`
	ISBN13        string   `json:"isbn13"`
	Authors       []string `json:"authors"`
	Publisher     string   `json:"publisher"`
	DatePublished string   `json:"date_published"`
	Synopsis      string   `json:"synopsis"`
	Language      string   `json:"language"`
	Image         string   `json:"image"`
	Pages         int      `json:"pages"`
}
