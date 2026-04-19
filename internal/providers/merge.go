// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package providers

import (
	"fmt"
	"sort"
	"strings"
)

// FieldOption is a single provider's value for a field, used as an alternative
// when providers disagree.
type FieldOption struct {
	Value         string `json:"value"`
	Source        string `json:"source"`
	SourceDisplay string `json:"source_display"`
}

// FieldResult is the merged value for one field: the primary value (from the
// highest-priority provider that has it), plus any alternatives from lower-
// priority providers that returned a different non-empty value.
// Alternatives is empty when all providers agree.
type FieldResult struct {
	Value         string        `json:"value"`
	Source        string        `json:"source"`
	SourceDisplay string        `json:"source_display"`
	Alternatives  []FieldOption `json:"alternatives"`
}

// CoverOption is a cover URL from a single provider.
type CoverOption struct {
	Source        string `json:"source"`
	SourceDisplay string `json:"source_display"`
	CoverURL      string `json:"cover_url"`
}

// MergedBookResult is the result of merging multiple BookResults by priority.
// Cover URLs are separated into the Covers slice and excluded from field-level
// comparison because they are binary (pick one, not compare text).
// A nil field pointer means no provider returned a value for that field.
type MergedBookResult struct {
	Title       *FieldResult `json:"title,omitempty"`
	Subtitle    *FieldResult `json:"subtitle,omitempty"`
	Authors     *FieldResult `json:"authors,omitempty"` // value is comma-joined author names
	Description *FieldResult `json:"description,omitempty"`
	Publisher   *FieldResult `json:"publisher,omitempty"`
	PublishDate *FieldResult `json:"publish_date,omitempty"`
	Language    *FieldResult `json:"language,omitempty"`
	ISBN10      *FieldResult `json:"isbn_10,omitempty"`
	ISBN13      *FieldResult `json:"isbn_13,omitempty"`
	PageCount   *FieldResult `json:"page_count,omitempty"`
	// Categories is the union of all providers' category tags, used for
	// genre/media-type inference. Not shown in the UI as a mergeable field.
	Categories []string `json:"categories"`
	// Covers lists available cover images from each provider, in priority order.
	Covers []CoverOption `json:"covers"`
}

// MergeBookResults combines results from multiple providers using the given
// priority order. Providers not present in priorityOrder are ranked last.
func MergeBookResults(results []*BookResult, priorityOrder []string) *MergedBookResult {
	merged := &MergedBookResult{}
	if len(results) == 0 {
		return merged
	}

	sorted := sortByPriority(results, priorityOrder)

	// Union of all categories (preserve provider priority order).
	catSeen := make(map[string]bool)
	for _, r := range sorted {
		for _, c := range r.Categories {
			key := strings.ToLower(c)
			if !catSeen[key] {
				catSeen[key] = true
				merged.Categories = append(merged.Categories, c)
			}
		}
	}

	// Cover options — one entry per distinct URL, in priority order.
	coverSeen := make(map[string]bool)
	for _, r := range sorted {
		if r.CoverURL != "" && !coverSeen[r.CoverURL] {
			coverSeen[r.CoverURL] = true
			merged.Covers = append(merged.Covers, CoverOption{
				Source:        r.Provider,
				SourceDisplay: r.ProviderDisplay,
				CoverURL:      r.CoverURL,
			})
		}
	}

	// String fields.
	merged.Title = mergeStringField(sorted, func(r *BookResult) string { return r.Title })
	merged.Subtitle = mergeStringField(sorted, func(r *BookResult) string { return r.Subtitle })
	merged.Authors = mergeStringField(sorted, func(r *BookResult) string { return strings.Join(r.Authors, ", ") })
	merged.Description = mergeStringField(sorted, func(r *BookResult) string { return r.Description })
	merged.Publisher = mergeStringField(sorted, func(r *BookResult) string { return r.Publisher })
	merged.PublishDate = mergeStringField(sorted, func(r *BookResult) string { return r.PublishDate })
	merged.Language = mergeStringField(sorted, func(r *BookResult) string { return r.Language })
	merged.ISBN10 = mergeStringField(sorted, func(r *BookResult) string { return r.ISBN10 })
	merged.ISBN13 = mergeStringField(sorted, func(r *BookResult) string { return r.ISBN13 })

	// Page count (pointer field — convert to string for FieldResult).
	merged.PageCount = mergeStringField(sorted, func(r *BookResult) string {
		if r.PageCount == nil {
			return ""
		}
		return fmt.Sprintf("%d", *r.PageCount)
	})

	return merged
}

// mergeStringField returns a FieldResult for a single string field. The
// primary value comes from the highest-priority provider that has it.
// Providers with a different non-empty value are listed as alternatives.
// Returns nil if no provider had a value.
func mergeStringField(sorted []*BookResult, get func(*BookResult) string) *FieldResult {
	var primary *FieldResult
	for _, r := range sorted {
		val := strings.TrimSpace(get(r))
		if val == "" {
			continue
		}
		if primary == nil {
			primary = &FieldResult{
				Value:         val,
				Source:        r.Provider,
				SourceDisplay: r.ProviderDisplay,
				Alternatives:  []FieldOption{},
			}
			continue
		}
		// Add as alternative only if meaningfully different (case-insensitive).
		if !strings.EqualFold(val, primary.Value) {
			primary.Alternatives = append(primary.Alternatives, FieldOption{
				Value:         val,
				Source:        r.Provider,
				SourceDisplay: r.ProviderDisplay,
			})
		}
	}
	return primary
}

// sortByPriority returns a new slice sorted by the given priority order.
// Providers not in the order list are ranked after those that are.
func sortByPriority(results []*BookResult, order []string) []*BookResult {
	rank := make(map[string]int, len(order))
	for i, name := range order {
		rank[name] = i
	}
	sorted := make([]*BookResult, len(results))
	copy(sorted, results)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, ok1 := rank[sorted[i].Provider]
		rj, ok2 := rank[sorted[j].Provider]
		if !ok1 {
			ri = len(order)
		}
		if !ok2 {
			rj = len(order)
		}
		return ri < rj
	})
	return sorted
}
