// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

// Package manga contains manga metadata providers.
package manga

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/fireball1725/librarium-api/internal/providers"
)

// MangaDexProvider searches for manga series via the MangaDex public API.
// Free, no API key required.
type MangaDexProvider struct {
	client  *http.Client
	enabled bool
}

func NewMangaDexProvider() *MangaDexProvider {
	return &MangaDexProvider{
		client:  &http.Client{},
		enabled: true, // enabled by default (free, no key)
	}
}

func (p *MangaDexProvider) Info() providers.ProviderInfo {
	return providers.ProviderInfo{
		Name:         "mangadex",
		DisplayName:  "MangaDex",
		Description:  "Manga series metadata from MangaDex. Free, no API key required.",
		RequiresKey:  false,
		Capabilities: []string{providers.CapSeriesName, providers.CapSeriesVolumes},
	}
}

func (p *MangaDexProvider) Configure(cfg map[string]string) {
	if v, ok := cfg["enabled"]; ok {
		p.enabled = v != "false"
	} else {
		p.enabled = true
	}
}

func (p *MangaDexProvider) Enabled() bool { return p.enabled }

func (p *MangaDexProvider) SearchSeries(ctx context.Context, query string) ([]providers.SeriesResult, error) {
	q := url.Values{}
	q.Set("title", query)
	q.Set("limit", "10")
	q.Set("includes[]", "cover_art")
	q.Add("contentRating[]", "safe")
	q.Add("contentRating[]", "suggestive")
	q.Add("contentRating[]", "erotica")

	apiURL := "https://api.mangadex.org/manga?" + q.Encode()
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
		return nil, fmt.Errorf("mangadex returned status %d", resp.StatusCode)
	}

	var body mdResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	var out []providers.SeriesResult
	for _, m := range body.Data {
		title := mdLocalizedString(m.Attributes.Title)
		desc := mdLocalizedString(m.Attributes.Description)
		status := m.Attributes.Status

		result := providers.SeriesResult{
			Provider:         "mangadex",
			ProviderDisplay:  "MangaDex",
			Name:             title,
			Description:      desc,
			IsComplete:       status == "completed",
			ExternalID:       m.ID,
			ExternalSource:   "mangadex",
			CoverURL:         mdCoverURL(m),
			Status:           status,
			OriginalLanguage: m.Attributes.OriginalLanguage,
			Demographic:      m.Attributes.PublicationDemographic,
			URL:              fmt.Sprintf("https://mangadex.org/title/%s", m.ID),
		}
		if m.Attributes.LastVolume != "" {
			if n, err := strconv.Atoi(m.Attributes.LastVolume); err == nil {
				result.TotalCount = &n
			}
		}
		if m.Attributes.Year != nil {
			result.PublicationYear = m.Attributes.Year
		}
		var genres []string
		for _, tag := range m.Attributes.Tags {
			if tag.Attributes.Group == "genre" {
				if name, ok := tag.Attributes.Name["en"]; ok && name != "" {
					genres = append(genres, name)
				}
			}
		}
		result.Genres = genres
		out = append(out, result)
	}
	return out, nil
}

// FetchSeriesVolumes retrieves per-volume metadata for a MangaDex manga.
func (p *MangaDexProvider) FetchSeriesVolumes(ctx context.Context, externalID string) ([]providers.VolumeResult, error) {
	// Step 1: fetch aggregate to get the authoritative list of volume positions.
	// The aggregate endpoint is reliable for volume numbers even when specific
	// chapter IDs may point to deleted/unavailable scanlations.
	aggURL := fmt.Sprintf("https://api.mangadex.org/manga/%s/aggregate", externalID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, aggURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mangadex aggregate returned status %d", resp.StatusCode)
	}
	var agg mdAggregateResponse
	if err := json.NewDecoder(resp.Body).Decode(&agg); err != nil {
		return nil, err
	}

	volumePositions := map[float64]bool{}
	for volKey := range agg.Volumes {
		if volKey == "none" {
			continue
		}
		pos, err := strconv.ParseFloat(volKey, 64)
		if err != nil {
			continue
		}
		volumePositions[pos] = true
	}

	if len(volumePositions) == 0 {
		return nil, nil
	}

	// Step 2: search chapters by manga ID ordered by volume/chapter, paginating up to
	// 500 results. We group by chapter.attributes.volume and keep the earliest
	// publishAt per volume — this avoids the deleted-chapter-ID problem entirely.
	earliestDateByVolume := map[float64]string{}
	const pageSize = 100
	const maxPages = 5

	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("manga", externalID)
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("offset", strconv.Itoa(page*pageSize))
		q.Add("order[volume]", "asc")
		q.Add("order[chapter]", "asc")
		q.Add("contentRating[]", "safe")
		q.Add("contentRating[]", "suggestive")
		q.Add("contentRating[]", "erotica")

		chReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.mangadex.org/chapter?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		chResp, err := p.client.Do(chReq)
		if err != nil {
			return nil, err
		}
		var chBody mdChaptersResponse
		err = json.NewDecoder(chResp.Body).Decode(&chBody)
		chResp.Body.Close()
		if err != nil {
			return nil, err
		}

		for _, ch := range chBody.Data {
			volStr := ch.Attributes.Volume
			if volStr == "" || ch.Attributes.PublishAt == "" {
				continue
			}
			pos, err := strconv.ParseFloat(volStr, 64)
			if err != nil {
				continue
			}
			date := ch.Attributes.PublishAt
			if len(date) >= 10 {
				date = date[:10]
			}
			// Keep the earliest date seen for this volume
			if existing, ok := earliestDateByVolume[pos]; !ok || date < existing {
				earliestDateByVolume[pos] = date
			}
		}

		if len(chBody.Data) < pageSize {
			break // no more pages
		}
	}

	// Step 3: build results from the authoritative volume position set
	results := make([]providers.VolumeResult, 0, len(volumePositions))
	for pos := range volumePositions {
		vr := providers.VolumeResult{Position: pos}
		if d, ok := earliestDateByVolume[pos]; ok {
			vr.ReleaseDate = d
		}
		results = append(results, vr)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Position < results[j].Position
	})
	return results, nil
}

// ─── MangaDex API types ───────────────────────────────────────────────────────

type mdResponse struct {
	Data []mdManga `json:"data"`
}

type mdManga struct {
	ID           string          `json:"id"`
	Attributes   mdMangaAttrs    `json:"attributes"`
	Relationships []mdRelationship `json:"relationships"`
}

type mdMangaAttrs struct {
	Title                  map[string]string `json:"title"`
	Description            map[string]string `json:"description"`
	Status                 string            `json:"status"`
	LastVolume             string            `json:"lastVolume"`
	OriginalLanguage       string            `json:"originalLanguage"`
	Year                   *int              `json:"year"`
	PublicationDemographic string            `json:"publicationDemographic"`
	Tags                   []mdTag           `json:"tags"`
}

type mdTag struct {
	Attributes mdTagAttrs `json:"attributes"`
}

type mdTagAttrs struct {
	Name  map[string]string `json:"name"`
	Group string            `json:"group"`
}

type mdRelationship struct {
	Type       string         `json:"type"`
	ID         string         `json:"id"`
	Attributes *mdCoverAttrs  `json:"attributes,omitempty"`
}

type mdCoverAttrs struct {
	FileName string `json:"fileName"`
}

type mdAggregateResponse struct {
	Volumes map[string]mdAggregateVolume `json:"volumes"`
}

type mdAggregateVolume struct {
	Volume   string                        `json:"volume"`
	Chapters map[string]mdAggregateChapter `json:"chapters"`
}

type mdAggregateChapter struct {
	Chapter string `json:"chapter"`
	ID      string `json:"id"`
}

type mdChaptersResponse struct {
	Data []mdChapterData `json:"data"`
}

type mdChapterData struct {
	ID         string             `json:"id"`
	Attributes mdChapterAttributes `json:"attributes"`
}

type mdChapterAttributes struct {
	Volume    string `json:"volume"`
	PublishAt string `json:"publishAt"`
}

func mdLocalizedString(m map[string]string) string {
	if s, ok := m["en"]; ok && s != "" {
		return s
	}
	for _, v := range m {
		if v != "" {
			return v
		}
	}
	return ""
}

func mdCoverURL(m mdManga) string {
	for _, rel := range m.Relationships {
		if rel.Type == "cover_art" && rel.Attributes != nil {
			return fmt.Sprintf("https://uploads.mangadex.org/covers/%s/%s.256.jpg", m.ID, rel.Attributes.FileName)
		}
	}
	return ""
}
