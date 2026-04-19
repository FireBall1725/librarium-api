// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package books

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fireball1725/librarium-api/internal/providers"
)

// SearchContributors searches Open Library for authors by name.
// Returns up to 10 candidates with external ID (OL key), name, and photo.
func (p *OpenLibraryProvider) SearchContributors(ctx context.Context, name string) ([]*providers.ContributorSearchResult, error) {
	u := "https://openlibrary.org/search/authors.json?q=" + url.QueryEscape(name) + "&limit=10"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("open_library author search: %w", err)
	}
	defer resp.Body.Close()

	var body struct {
		Docs []struct {
			Key     string  `json:"key"` // "/authors/OL23919A"
			Name    string  `json:"name"`
			Photos  []int64 `json:"photos"` // array of photo IDs; negative = no photo
			TopWork string  `json:"top_work"`
		} `json:"docs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("open_library author search decode: %w", err)
	}

	var out []*providers.ContributorSearchResult
	for _, d := range body.Docs {
		if d.Name == "" {
			continue
		}
		// Key is "/authors/OL23919A" — strip the prefix
		externalID := strings.TrimPrefix(d.Key, "/authors/")
		result := &providers.ContributorSearchResult{
			ExternalID: externalID,
			Name:       d.Name,
		}
		for _, pid := range d.Photos {
			if pid > 0 {
				result.PhotoURL = fmt.Sprintf("https://covers.openlibrary.org/a/id/%d-M.jpg", pid)
				break
			}
		}
		out = append(out, result)
	}
	return out, nil
}

// FetchContributor fetches the full author profile and bibliography from Open Library.
// externalID is the OL author key without "/authors/" prefix (e.g. "OL23919A").
func (p *OpenLibraryProvider) FetchContributor(ctx context.Context, externalID string) (*providers.ContributorData, error) {
	// Fetch author detail and works concurrently.
	type authorOut struct {
		data *olAuthor
		err  error
	}
	type worksOut struct {
		works []olWork
		err   error
	}

	authorCh := make(chan authorOut, 1)
	worksCh := make(chan worksOut, 1)

	go func() {
		u := "https://openlibrary.org/authors/" + externalID + ".json"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			authorCh <- authorOut{err: err}
			return
		}
		resp, err := p.client.Do(req)
		if err != nil {
			authorCh <- authorOut{err: err}
			return
		}
		defer resp.Body.Close()
		var a olAuthor
		if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
			authorCh <- authorOut{err: err}
			return
		}
		authorCh <- authorOut{data: &a}
	}()

	go func() {
		u := "https://openlibrary.org/authors/" + externalID + "/works.json?limit=200"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			worksCh <- worksOut{err: err}
			return
		}
		resp, err := p.client.Do(req)
		if err != nil {
			worksCh <- worksOut{err: err}
			return
		}
		defer resp.Body.Close()
		var body struct {
			Entries []olWork `json:"entries"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			worksCh <- worksOut{err: err}
			return
		}
		worksCh <- worksOut{works: body.Entries}
	}()

	ar := <-authorCh
	wr := <-worksCh

	if ar.err != nil {
		return nil, fmt.Errorf("open_library fetch author: %w", ar.err)
	}
	// Works failure is non-fatal — return what we have.

	a := ar.data
	cd := &providers.ContributorData{
		Provider:   "open_library",
		ExternalID: externalID,
		Name:       a.Name,
		Bio:        olExtractText(a.Bio),
	}

	// Photo: use first positive photo ID
	for _, pid := range a.Photos {
		if pid > 0 {
			cd.PhotoURL = fmt.Sprintf("https://covers.openlibrary.org/a/id/%d-L.jpg", pid)
			break
		}
	}

	// Dates — OL stores as free-form strings like "31 July 1965" or "1965"
	if a.BirthDate != "" {
		if t := olParsePersonDate(a.BirthDate); t != nil {
			cd.BornDate = t
		}
	}
	if a.DeathDate != "" {
		if t := olParsePersonDate(a.DeathDate); t != nil {
			cd.DiedDate = t
		}
	}

	// Works
	for _, w := range wr.works {
		if w.Title == "" {
			continue
		}
		wr := providers.ContributorWorkResult{
			Title: w.Title,
		}
		if len(w.Covers) > 0 && w.Covers[0] > 0 {
			wr.CoverURL = fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-M.jpg", w.Covers[0])
		}
		if w.FirstPublishDate != "" {
			if y, err := strconv.Atoi(w.FirstPublishDate[:4]); err == nil {
				wr.PublishYear = &y
			}
		}
		cd.Works = append(cd.Works, wr)
	}

	return cd, nil
}

// ─── Open Library author types ────────────────────────────────────────────────

type olAuthor struct {
	Key       string          `json:"key"`
	Name      string          `json:"name"`
	Bio       json.RawMessage `json:"bio"`        // string or {"type":"...","value":"..."}
	BirthDate string          `json:"birth_date"`
	DeathDate string          `json:"death_date"`
	Photos    []int64         `json:"photos"`
}

type olWork struct {
	Title            string  `json:"title"`
	Covers           []int64 `json:"covers"`
	FirstPublishDate string  `json:"first_publish_date"`
}

// olExtractText handles OL fields that are either a plain string or
// {"type": "/type/text", "value": "..."}.
func olExtractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Value
	}
	return ""
}

// olParsePersonDate parses OL author date strings like "31 July 1965", "July 31, 1965",
// "1965", "circa 1820", etc. Returns nil if unparseable.
func olParsePersonDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "circa ")
	s = strings.TrimPrefix(s, "c. ")

	// Year only
	if len(s) == 4 {
		if t, err := time.Parse("2006", s); err == nil {
			return &t
		}
	}
	// "YYYY-MM-DD"
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return &t
	}
	// "D Month YYYY"
	for _, layout := range []string{"2 January 2006", "2 Jan 2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	// "Month D, YYYY"
	for _, layout := range []string{"January 2, 2006", "Jan 2, 2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	// "Month YYYY"
	for _, layout := range []string{"January 2006", "Jan 2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

