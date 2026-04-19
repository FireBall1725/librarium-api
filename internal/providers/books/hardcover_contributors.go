// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package books

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/fireball1725/librarium-api/internal/providers"
)

const hardcoverAuthorSearchQuery = `
query SearchAuthors($query: String!) {
  search(query: $query, query_type: "Author", per_page: 10) {
    results
  }
}`

// SearchContributors searches Hardcover for authors by name.
func (p *HardcoverProvider) SearchContributors(ctx context.Context, name string) ([]*providers.ContributorSearchResult, error) {
	payload, _ := json.Marshal(hcGQLRequest{
		Query:     hardcoverAuthorSearchQuery,
		Variables: map[string]any{"query": name},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hardcoverEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("hardcover: invalid API key")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hardcover author search: status %d", resp.StatusCode)
	}

	var body hcSearchGQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if len(body.Errors) > 0 {
		return nil, fmt.Errorf("hardcover author search: %s", body.Errors[0].Message)
	}

	docs, err := parseHardcoverAuthorDocs(body.Data.Search.Results)
	if err != nil {
		return nil, nil
	}

	var out []*providers.ContributorSearchResult
	for _, d := range docs {
		if d.Name == "" && d.Slug == "" {
			continue
		}
		out = append(out, &providers.ContributorSearchResult{
			ExternalID: d.Slug,
			Name:       d.Name,
			PhotoURL:   d.photoURL(),
		})
	}
	return out, nil
}

const hardcoverAuthorDetailQuery = `
query GetAuthor($slug: String!) {
  authors(where: {slug: {_eq: $slug}}, limit: 1) {
    id
    name
    bio
    image { url }
    contributions(limit: 200) {
      book {
        title
        release_date
        editions(limit: 5) {
          isbn_13
          isbn_10
          image { url }
        }
      }
    }
  }
}`

// FetchContributor fetches full author profile + bibliography from Hardcover.
// externalID is the author slug (e.g. "andy-weir").
func (p *HardcoverProvider) FetchContributor(ctx context.Context, externalID string) (*providers.ContributorData, error) {
	payload, _ := json.Marshal(hcGQLRequest{
		Query:     hardcoverAuthorDetailQuery,
		Variables: map[string]any{"slug": externalID},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hardcoverEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("hardcover: invalid API key")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hardcover fetch author: status %d", resp.StatusCode)
	}

	var body hcAuthorDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if len(body.Errors) > 0 {
		// Log each error on its own line so it is never truncated in the terminal.
		for i, e := range body.Errors {
			slog.ErrorContext(ctx, "hardcover author GQL error", "index", i, "message", e.Message)
		}
		return nil, fmt.Errorf("hardcover fetch author: %s", body.Errors[0].Message)
	}
	if len(body.Data.Authors) == 0 {
		return nil, nil
	}

	a := body.Data.Authors[0]
	cd := &providers.ContributorData{
		Provider:   "hardcover",
		ExternalID: externalID,
		Name:       a.Name,
		Bio:        a.Bio,
	}
	if a.Image != nil && a.Image.URL != "" {
		cd.PhotoURL = a.Image.URL
	}

	var isbnCount int
	for _, ab := range a.Contributions {
		if ab.Book == nil || ab.Book.Title == "" {
			continue
		}
		w := providers.ContributorWorkResult{Title: ab.Book.Title}
		if ab.Book.ReleaseDate != "" && len(ab.Book.ReleaseDate) >= 4 {
			if y, err := strconv.Atoi(ab.Book.ReleaseDate[:4]); err == nil {
				w.PublishYear = &y
			}
		}
		if len(ab.Book.Editions) > 0 {
			// Prefer the first edition that has an ISBN; fall back to first for cover.
			best := &ab.Book.Editions[0]
			for i := range ab.Book.Editions {
				ed := &ab.Book.Editions[i]
				if ed.ISBN13 != "" || ed.ISBN10 != "" {
					best = ed
					break
				}
			}
			w.ISBN13 = best.ISBN13
			w.ISBN10 = best.ISBN10
			if best.Image != nil {
				w.CoverURL = best.Image.URL
			}
		}
		if w.ISBN13 != "" || w.ISBN10 != "" {
			isbnCount++
		}
		cd.Works = append(cd.Works, w)
	}
	slog.InfoContext(ctx, "hardcover author works fetched",
		"author", a.Name, "total_works", len(cd.Works), "works_with_isbn", isbnCount)

	return cd, nil
}


func parseHardcoverAuthorDocs(raw json.RawMessage) ([]hcAuthorSearchDoc, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var wrapper struct {
		Hits []struct {
			Document hcAuthorSearchDoc `json:"document"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper.Hits) > 0 {
		out := make([]hcAuthorSearchDoc, 0, len(wrapper.Hits))
		for _, h := range wrapper.Hits {
			out = append(out, h.Document)
		}
		return out, nil
	}
	var docs []hcAuthorSearchDoc
	if err := json.Unmarshal(raw, &docs); err == nil {
		return docs, nil
	}
	return nil, fmt.Errorf("unrecognised author search results shape")
}

// ─── Hardcover author types ───────────────────────────────────────────────────

// hcAuthorSearchDoc is one author document from the Typesense search results.
// Hardcover may return the image as a flat "image_url" string or as a nested {"url":"..."} object.
type hcAuthorSearchDoc struct {
	Name     string   `json:"name"`
	Slug     string   `json:"slug"`
	ImageURL string   `json:"image_url"` // flat form
	Image    *hcImage `json:"image"`     // nested form
}

func (d *hcAuthorSearchDoc) photoURL() string {
	if d.ImageURL != "" {
		return d.ImageURL
	}
	if d.Image != nil && d.Image.URL != "" {
		return d.Image.URL
	}
	return ""
}

type hcAuthorDetailResponse struct {
	Data struct {
		Authors []hcAuthorDetail `json:"authors"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type hcAuthorDetail struct {
	ID            int              `json:"id"`
	Name          string           `json:"name"`
	Bio           string           `json:"bio"`
	Image         *hcImage         `json:"image"`
	Contributions []hcAuthorBook   `json:"contributions"`
}

type hcAuthorBook struct {
	Book *hcAuthorBookData `json:"book"`
}

type hcAuthorBookData struct {
	Title       string          `json:"title"`
	ReleaseDate string          `json:"release_date"`
	Editions    []hcEditionISBN `json:"editions"`
}

type hcEditionISBN struct {
	ISBN13 string   `json:"isbn_13"`
	ISBN10 string   `json:"isbn_10"`
	Image  *hcImage `json:"image"`
}
