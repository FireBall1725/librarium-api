// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

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

// OpenLibraryProvider looks up books via the Open Library APIs.
// Free, no API key required.
//
// Two requests are made concurrently per lookup:
//  1. Books API (jscmd=data) — author names, cover URLs, page count, ISBNs
//  2. Edition JSON + Works JSON — description, language, publish date
type OpenLibraryProvider struct {
	base
	client *http.Client
}

func NewOpenLibraryProvider() *OpenLibraryProvider {
	return &OpenLibraryProvider{
		base:   base{enabled: true},
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *OpenLibraryProvider) Info() providers.ProviderInfo {
	return providers.ProviderInfo{
		Name:         "open_library",
		DisplayName:  "Open Library",
		Description:  "Free book metadata from the Internet Archive. No API key required.",
		RequiresKey:  false,
		Capabilities: []string{providers.CapBookISBN, providers.CapBookSearch, providers.CapContributor},
	}
}

func (p *OpenLibraryProvider) Configure(cfg map[string]string) {
	if v, ok := cfg["enabled"]; ok {
		p.enabled = v != "false"
	} else {
		p.enabled = true
	}
}

func (p *OpenLibraryProvider) LookupByISBN(ctx context.Context, isbn string) (*providers.BookResult, error) {
	type dataOut struct {
		book *olBook
		err  error
	}
	type edOut struct {
		ed  *olEdition
		err error
	}

	dataCh := make(chan dataOut, 1)
	edCh := make(chan edOut, 1)

	// Request 1: Books API (jscmd=data) — author names, covers, identifiers
	go func() {
		url := fmt.Sprintf("https://openlibrary.org/api/books?bibkeys=ISBN:%s&jscmd=data&format=json", isbn)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			dataCh <- dataOut{nil, err}
			return
		}
		resp, err := p.client.Do(req)
		if err != nil {
			dataCh <- dataOut{nil, err}
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			dataCh <- dataOut{nil, fmt.Errorf("open library jscmd=data: status %d", resp.StatusCode)}
			return
		}
		var raw map[string]olBook
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			dataCh <- dataOut{nil, err}
			return
		}
		if book, ok := raw["ISBN:"+isbn]; ok {
			dataCh <- dataOut{&book, nil}
		} else {
			dataCh <- dataOut{nil, nil}
		}
	}()

	// Request 2: Edition JSON — work key, language, publish date (sometimes more complete)
	go func() {
		url := fmt.Sprintf("https://openlibrary.org/isbn/%s.json", isbn)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			edCh <- edOut{nil, err}
			return
		}
		resp, err := p.client.Do(req)
		if err != nil {
			edCh <- edOut{nil, err}
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			edCh <- edOut{nil, nil} // not fatal
			return
		}
		var ed olEdition
		if err := json.NewDecoder(resp.Body).Decode(&ed); err != nil {
			edCh <- edOut{nil, nil}
			return
		}
		edCh <- edOut{&ed, nil}
	}()

	bookRes := <-dataCh
	edRes := <-edCh

	if bookRes.err != nil {
		return nil, bookRes.err
	}
	if bookRes.book == nil {
		return nil, nil
	}

	book := bookRes.book
	ed := edRes.ed // may be nil

	result := &providers.BookResult{
		Provider:        "open_library",
		ProviderDisplay: "Open Library",
		Title:           book.Title,
		Subtitle:        book.Subtitle,
		Publisher:       firstPublisher(book.Publishers),
		PublishDate:     normalizeDate(book.PublishDate),
		Description:     cleanDescription(olDescription(book.Notes)),
		Language:        olLanguage(book.Languages),
	}

	// Supplement from edition JSON where jscmd=data may be sparse
	if ed != nil {
		if result.PublishDate == "" {
			result.PublishDate = normalizeDate(ed.PublishDate)
		}
		if result.Language == "" {
			result.Language = olLanguage(ed.Languages)
		}
		if result.Publisher == "" && len(ed.Publishers) > 0 {
			result.Publisher = ed.Publishers[0]
		}
	}

	for _, a := range book.Authors {
		result.Authors = append(result.Authors, a.Name)
	}
	for _, id := range book.Identifiers.ISBN10 {
		result.ISBN10 = id
		break
	}
	for _, id := range book.Identifiers.ISBN13 {
		result.ISBN13 = id
		break
	}
	// Supplement ISBNs from edition if missing
	if ed != nil {
		if result.ISBN10 == "" && len(ed.ISBN10) > 0 {
			result.ISBN10 = ed.ISBN10[0]
		}
		if result.ISBN13 == "" && len(ed.ISBN13) > 0 {
			result.ISBN13 = ed.ISBN13[0]
		}
	}

	// Open Library returns the sentinel URL .../b/id/-1-M.jpg when no cover
	// exists — skip those so the batch enrichment doesn't waste its slot on
	// a guaranteed 404 when other providers might have a real cover.
	if isValidOLCover(book.Cover.Medium) {
		result.CoverURL = book.Cover.Medium
	} else if isValidOLCover(book.Cover.Large) {
		result.CoverURL = book.Cover.Large
	}
	if book.NumberOfPages > 0 {
		n := book.NumberOfPages
		result.PageCount = &n
	}
	if result.PageCount == nil && ed != nil && ed.NumberOfPages > 0 {
		n := ed.NumberOfPages
		result.PageCount = &n
	}

	// Request 3: Work JSON — the description lives here
	if result.Description == "" && ed != nil && len(ed.Works) > 0 {
		if desc := p.fetchWorkDescription(ctx, ed.Works[0].Key); desc != "" {
			result.Description = desc
		}
	}

	// Subjects from jscmd=data (richer) or edition JSON
	for _, s := range book.Subjects {
		if s.Name != "" {
			result.Categories = append(result.Categories, s.Name)
		}
	}
	if len(result.Categories) == 0 && ed != nil {
		result.Categories = ed.Subjects
	}

	return result, nil
}

func (p *OpenLibraryProvider) SearchBooks(ctx context.Context, query string) ([]*providers.BookResult, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", "15")
	params.Set("fields", "title,subtitle,author_name,publisher,first_publish_year,isbn,cover_i,language,subject")

	apiURL := "https://openlibrary.org/search.json?" + params.Encode()
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
		return nil, fmt.Errorf("open library search: status %d", resp.StatusCode)
	}

	var body olSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	var out []*providers.BookResult
	for _, doc := range body.Docs {
		if doc.Title == "" {
			continue
		}
		result := &providers.BookResult{
			Provider:        "open_library",
			ProviderDisplay: "Open Library",
			Title:           doc.Title,
			Subtitle:        doc.Subtitle,
			Authors:         doc.AuthorNames,
			Categories:      doc.Subject,
		}
		if len(doc.Publisher) > 0 {
			result.Publisher = doc.Publisher[0]
		}
		if doc.FirstPublishYear > 0 {
			result.PublishDate = fmt.Sprintf("%d-01-01", doc.FirstPublishYear)
		}
		for _, isbn := range doc.ISBN {
			switch len(isbn) {
			case 13:
				if result.ISBN13 == "" {
					result.ISBN13 = isbn
				}
			case 10:
				if result.ISBN10 == "" {
					result.ISBN10 = isbn
				}
			}
		}
		if doc.CoverI > 0 {
			result.CoverURL = fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-M.jpg", doc.CoverI)
		}
		if len(doc.Language) > 0 {
			result.Language = doc.Language[0]
		}
		out = append(out, result)
	}
	return out, nil
}

func (p *OpenLibraryProvider) fetchWorkDescription(ctx context.Context, workKey string) string {
	url := "https://openlibrary.org" + workKey + ".json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := p.client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return ""
	}
	defer resp.Body.Close()

	var work struct {
		Description any `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&work); err != nil {
		return ""
	}
	return cleanDescription(olDescription(work.Description))
}

// ─── Open Library API types ───────────────────────────────────────────────────

// olBook is the jscmd=data response shape — rich author names, cover URLs.
type olBook struct {
	Title         string       `json:"title"`
	Subtitle      string       `json:"subtitle"`
	Authors       []olName     `json:"authors"`
	Publishers    []olName     `json:"publishers"`
	PublishDate   string       `json:"publish_date"`
	NumberOfPages int          `json:"number_of_pages"`
	Identifiers   olIdentifier `json:"identifiers"`
	Cover         olCover      `json:"cover"`
	Languages     []olKey      `json:"languages"`
	Notes         any          `json:"notes"` // string or {"type","value"}
	Subjects      []olName     `json:"subjects"`
}

// olEdition is the /isbn/{isbn}.json shape — has work key and sometimes more complete metadata.
type olEdition struct {
	Title         string   `json:"title"`
	PublishDate   string   `json:"publish_date"`
	Publishers    []string `json:"publishers"`
	Languages     []olKey  `json:"languages"`
	NumberOfPages int      `json:"number_of_pages"`
	ISBN10        []string `json:"isbn_10"`
	ISBN13        []string `json:"isbn_13"`
	Works         []olKey  `json:"works"`
	Subjects      []string `json:"subjects"`
}

type olName struct{ Name string }
type olKey struct{ Key string }

type olIdentifier struct {
	ISBN10 []string `json:"isbn_10"`
	ISBN13 []string `json:"isbn_13"`
}

type olCover struct {
	Small  string `json:"small"`
	Medium string `json:"medium"`
	Large  string `json:"large"`
}

// olSearchResponse is the /search.json response shape.
type olSearchResponse struct {
	NumFound int           `json:"numFound"`
	Docs     []olSearchDoc `json:"docs"`
}

type olSearchDoc struct {
	Title            string   `json:"title"`
	Subtitle         string   `json:"subtitle"`
	AuthorNames      []string `json:"author_name"`
	Publisher        []string `json:"publisher"`
	FirstPublishYear int      `json:"first_publish_year"`
	ISBN             []string `json:"isbn"`
	CoverI           int      `json:"cover_i"`
	Language         []string `json:"language"`
	Subject          []string `json:"subject"`
}

func firstPublisher(pubs []olName) string {
	if len(pubs) > 0 {
		return pubs[0].Name
	}
	return ""
}

func olDescription(notes any) string {
	switch v := notes.(type) {
	case string:
		return v
	case map[string]any:
		if s, ok := v["value"].(string); ok {
			return s
		}
	}
	return ""
}

func olLanguage(langs []olKey) string {
	if len(langs) == 0 {
		return ""
	}
	// key is like "/languages/eng" — map 3-letter codes to ISO 639-1
	parts := strings.Split(langs[0].Key, "/")
	code := ""
	if len(parts) > 0 {
		code = parts[len(parts)-1]
	}
	iso := map[string]string{
		"eng": "en", "jpn": "ja", "fre": "fr", "ger": "de", "spa": "es",
		"ita": "it", "por": "pt", "rus": "ru", "chi": "zh", "kor": "ko",
	}
	if v, ok := iso[code]; ok {
		return v
	}
	return code
}

// normalizeDate parses Open Library's free-text dates into YYYY-MM-DD.
// Returns the original string unchanged if it cannot be parsed.
var (
	reYearOnly  = regexp.MustCompile(`^\d{4}$`)
	reMonthYear = regexp.MustCompile(`^([A-Za-z]+)\s+(\d{4})$`)
	reFullDate  = regexp.MustCompile(`^([A-Za-z]+)\s+(\d{1,2}),?\s+(\d{4})$`)
	reDayFirst  = regexp.MustCompile(`^(\d{1,2})\s+([A-Za-z]+)\s+(\d{4})$`)
)

func normalizeDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, layout := range []string{"2006-01-02", "2006-01"} {
		if _, err := time.Parse(layout, s); err == nil {
			if layout == "2006-01" {
				return s + "-01"
			}
			return s
		}
	}
	if reYearOnly.MatchString(s) {
		return s + "-01-01"
	}
	// "Month D, YYYY" or "Mon D, YYYY" (full or abbreviated month)
	if m := reFullDate.FindStringSubmatch(s); m != nil {
		day := m[2]
		if len(day) == 1 {
			day = "0" + day
		}
		for _, mfmt := range []string{"January", "Jan"} {
			if t, err := time.Parse(mfmt+" 02 2006", m[1]+" "+day+" "+m[3]); err == nil {
				return t.Format("2006-01-02")
			}
		}
	}
	// "Month YYYY" or "Mon YYYY"
	if m := reMonthYear.FindStringSubmatch(s); m != nil {
		for _, mfmt := range []string{"January 2006", "Jan 2006"} {
			if t, err := time.Parse(mfmt, m[1]+" "+m[2]); err == nil {
				return t.Format("2006-01-02")
			}
		}
	}
	// "D Month YYYY" / "D Mon YYYY" (European day-first)
	if m := reDayFirst.FindStringSubmatch(s); m != nil {
		day := m[1]
		if len(day) == 1 {
			day = "0" + day
		}
		for _, mfmt := range []string{"January", "Jan"} {
			if t, err := time.Parse("02 "+mfmt+" 2006", day+" "+m[2]+" "+m[3]); err == nil {
				return t.Format("2006-01-02")
			}
		}
	}
	return "" // return empty rather than raw unparseable string
}

// isValidOLCover rejects the sentinel URLs Open Library returns when no cover
// image exists — e.g. https://covers.openlibrary.org/b/id/-1-M.jpg.
func isValidOLCover(url string) bool {
	if url == "" {
		return false
	}
	return !strings.Contains(url, "/b/id/-") && !strings.Contains(url, "/b/olid/-")
}

// cleanDescription strips unhelpful Open Library metadata noise.
func cleanDescription(s string) string {
	if s == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "source title:") {
		return ""
	}
	lines := strings.Split(s, "\n")
	var kept []string
	for _, line := range lines {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "source title:") {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}
