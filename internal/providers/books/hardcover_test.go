// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package books

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// roundTripFunc lets a plain function satisfy http.RoundTripper, so tests
// can intercept a provider's hardcoded endpoint without changing production
// code to accept an injectable base URL.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func TestNormalizeHardcoverVolumeDate(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"1965-06-01", "1965-06-01"},
		// Same failure mode as the ISFDB series-volumes bug this guards
		// against: a bare "YYYY-MM"/"YYYY" must not reach the caller
		// unchanged — series_volumes.release_date is a raw `::date` SQL
		// cast downstream and rejects it outright.
		{"1965-06", "1965-06-01"},
		{"1965", "1965-01-01"},
		{"not a date", ""},
	}
	for _, tc := range cases {
		if got := normalizeHardcoverVolumeDate(tc.in); got != tc.want {
			t.Errorf("normalizeHardcoverVolumeDate(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFetchSeriesVolumes_BookSeriesKey guards against reverting to the wrong
// GraphQL field name. "series_books" was never valid against Hardcover's
// actual schema (confirmed live via introspection: the real field is
// "book_series") — every call failed GraphQL validation outright, so this
// was a hard break for every Hardcover-linked series, not a data-quality
// edge case. The response body below is a trimmed real capture.
func TestFetchSeriesVolumes_BookSeriesKey(t *testing.T) {
	const body = `{
		"data": {
			"series": [{
				"id": 60563,
				"name": "Greater Foundation Universe",
				"books_count": 13,
				"book_series": [
					{"position": 1, "book": {"title": "I, Robot", "release_date": "1940-09-01", "image": null}},
					{"position": 3, "book": {"title": "The Naked Sun", "release_date": "1953-10-01", "image": {"url": "https://example.com/naked-sun.jpg"}}}
				]
			}]
		}
	}`

	p := &HardcoverProvider{
		base:   base{enabled: true},
		apiKey: "test",
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(body), nil
		})},
	}

	got, err := p.FetchSeriesVolumes(context.Background(), "greater-foundation-universe")
	if err != nil {
		t.Fatalf("FetchSeriesVolumes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d volumes, want 2 (query used the wrong field name if this is 0)", len(got))
	}
	if got[0].Title != "I, Robot" || got[0].ReleaseDate != "1940-09-01" {
		t.Errorf("volume 0 = %+v, want title=I, Robot release_date=1940-09-01", got[0])
	}
	if got[1].Title != "The Naked Sun" || got[1].CoverURL != "https://example.com/naked-sun.jpg" {
		t.Errorf("volume 1 = %+v, want title=The Naked Sun cover set", got[1])
	}
}

func TestNormalizeHardcoverKeyStripsBearerPrefix(t *testing.T) {
	const token = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.sig"

	cases := map[string]string{
		"bare token":         token,
		"prefixed":           "Bearer " + token,
		"lowercase prefix":   "bearer " + token,
		"mixed case prefix":  "BeArEr " + token,
		"surrounding spaces": "  Bearer " + token + "  ",
		"doubled prefix":     "Bearer Bearer " + token,
		"newline from paste": "Bearer " + token + "\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if got := normalizeHardcoverKey(input); got != token {
				t.Errorf("normalizeHardcoverKey(%q) = %q, want %q", input, got, token)
			}
		})
	}
}

// An empty or whitespace-only key must stay empty: Configure keys the
// provider's enabled flag off it, so returning anything non-empty would
// enable a provider with no credentials.
func TestNormalizeHardcoverKeyEmpty(t *testing.T) {
	for _, input := range []string{"", "   ", "Bearer ", "bearer   "} {
		if got := normalizeHardcoverKey(input); got != "" {
			t.Errorf("normalizeHardcoverKey(%q) = %q, want empty", input, got)
		}
	}
}

// Configure is the only place the key is normalised, so the doubled
// prefix must be gone by the time any request builds its header.
func TestConfigureNormalizesKey(t *testing.T) {
	p := NewHardcoverProvider()
	p.Configure(map[string]string{"api_key": "Bearer abc123"})
	if p.apiKey != "abc123" {
		t.Errorf("apiKey = %q, want %q", p.apiKey, "abc123")
	}
	if !p.enabled {
		t.Error("expected provider to be enabled with a key present")
	}
}

func TestConfigureLeavesProviderDisabledWithoutKey(t *testing.T) {
	p := NewHardcoverProvider()
	p.Configure(map[string]string{"api_key": "Bearer "})
	if p.apiKey != "" {
		t.Errorf("apiKey = %q, want empty", p.apiKey)
	}
	if p.enabled {
		t.Error("expected provider to stay disabled when the key is only a prefix")
	}
}
