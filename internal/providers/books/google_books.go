// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package books

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fireball1725/librarium-api/internal/providers"
)

// GoogleBooksProvider looks up books via the Google Books API.
// Requires an API key for reliable access (free tier: 1000 req/day).
type GoogleBooksProvider struct {
	base
	apiKey string
	client *http.Client
}

func NewGoogleBooksProvider() *GoogleBooksProvider {
	return &GoogleBooksProvider{
		base:   base{enabled: false},
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *GoogleBooksProvider) Info() providers.ProviderInfo {
	return providers.ProviderInfo{
		Name:         "google_books",
		DisplayName:  "Google Books",
		Description:  "Google's book metadata API. Free tier: 1,000 requests/day with an API key.",
		RequiresKey:  true,
		Capabilities: []string{providers.CapBookISBN, providers.CapBookSearch},
		HelpText:     "Create a project in Google Cloud Console, enable the Books API, then create an API key under APIs & Services → Credentials. Restrict the key to the Books API for security.",
		HelpURL:      "https://console.cloud.google.com/apis/library/books.googleapis.com",
	}
}

func (p *GoogleBooksProvider) Configure(cfg map[string]string) {
	p.apiKey = cfg["api_key"]
	if v, ok := cfg["enabled"]; ok {
		p.enabled = v == "true" && p.apiKey != ""
	} else {
		p.enabled = p.apiKey != ""
	}
}

func (p *GoogleBooksProvider) LookupByISBN(ctx context.Context, isbn string) (*providers.BookResult, error) {
	q := url.Values{}
	q.Set("q", "isbn:"+isbn)
	if p.apiKey != "" {
		q.Set("key", p.apiKey)
	}

	apiURL := "https://www.googleapis.com/books/v1/volumes?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google books returned status %d", resp.StatusCode)
	}

	var body gbResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	if body.TotalItems == 0 || len(body.Items) == 0 {
		return nil, nil
	}

	vol := body.Items[0].VolumeInfo
	result := &providers.BookResult{
		Provider:        "google_books",
		ProviderDisplay: "Google Books",
		Title:           vol.Title,
		Subtitle:        vol.Subtitle,
		Authors:         vol.Authors,
		Publisher:       vol.Publisher,
		PublishDate:     normalizeDate(vol.PublishedDate),
		Description:     vol.Description,
		Language:        vol.Language,
	}

	for _, id := range vol.IndustryIdentifiers {
		switch id.Type {
		case "ISBN_10":
			result.ISBN10 = id.Identifier
		case "ISBN_13":
			result.ISBN13 = id.Identifier
		}
	}

	if vol.PageCount > 0 {
		n := vol.PageCount
		result.PageCount = &n
	}

	if vol.ImageLinks.Thumbnail != "" {
		// Use HTTPS and remove zoom restriction
		result.CoverURL = strings.Replace(vol.ImageLinks.Thumbnail, "http://", "https://", 1)
	}

	if len(vol.Categories) > 0 {
		result.Categories = vol.Categories
	}

	return result, nil
}

func (p *GoogleBooksProvider) SearchBooks(ctx context.Context, query string) ([]*providers.BookResult, error) {
	q := url.Values{}
	q.Set("q", query)
	q.Set("maxResults", "15")
	if p.apiKey != "" {
		q.Set("key", p.apiKey)
	}

	apiURL := "https://www.googleapis.com/books/v1/volumes?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google books search: status %d", resp.StatusCode)
	}

	var body gbResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	var out []*providers.BookResult
	for _, item := range body.Items {
		vol := item.VolumeInfo
		if vol.Title == "" {
			continue
		}
		result := &providers.BookResult{
			Provider:        "google_books",
			ProviderDisplay: "Google Books",
			Title:           vol.Title,
			Subtitle:        vol.Subtitle,
			Authors:         vol.Authors,
			Publisher:       vol.Publisher,
			PublishDate:     normalizeDate(vol.PublishedDate),
			Description:     vol.Description,
			Language:        vol.Language,
		}
		for _, id := range vol.IndustryIdentifiers {
			switch id.Type {
			case "ISBN_10":
				result.ISBN10 = id.Identifier
			case "ISBN_13":
				result.ISBN13 = id.Identifier
			}
		}
		if vol.PageCount > 0 {
			n := vol.PageCount
			result.PageCount = &n
		}
		if vol.ImageLinks.Thumbnail != "" {
			result.CoverURL = strings.Replace(vol.ImageLinks.Thumbnail, "http://", "https://", 1)
		}
		if len(vol.Categories) > 0 {
			result.Categories = vol.Categories
		}
		out = append(out, result)
	}
	return out, nil
}

// ─── Google Books API types ───────────────────────────────────────────────────

type gbResponse struct {
	TotalItems int      `json:"totalItems"`
	Items      []gbItem `json:"items"`
}

type gbItem struct {
	VolumeInfo gbVolumeInfo `json:"volumeInfo"`
}

type gbVolumeInfo struct {
	Title               string       `json:"title"`
	Subtitle            string       `json:"subtitle"`
	Authors             []string     `json:"authors"`
	Publisher           string       `json:"publisher"`
	PublishedDate       string       `json:"publishedDate"`
	Description         string       `json:"description"`
	IndustryIdentifiers []gbISBN     `json:"industryIdentifiers"`
	PageCount           int          `json:"pageCount"`
	Language            string       `json:"language"`
	ImageLinks          gbImageLinks `json:"imageLinks"`
	Categories          []string     `json:"categories"`
}

type gbISBN struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
}

type gbImageLinks struct {
	Thumbnail string `json:"thumbnail"`
}
