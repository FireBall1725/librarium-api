// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package providers

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/fireball1725/librarium-api/internal/models"
)

// FieldOption is one distinct value for a field and who gave it, used as an
// alternative to the pre-selected value.
type FieldOption struct {
	Value         string `json:"value"`
	Source        string `json:"source"`
	SourceDisplay string `json:"source_display"`
	// Sources is every provider that gave this value, first to answer first.
	Sources []string `json:"sources,omitempty"`
	// Values is the list behind a joined value; set for authors only.
	Values []string `json:"values,omitempty"`
}

// Why a field's value was pre-selected. The client shows this next to it.
const (
	ReasonAgreed     = "agreed"      // two or more providers gave it, more than any other value
	ReasonOnly       = "only"        // one provider had the field at all
	ReasonLongest    = "longest"     // descriptions: no agreement, so the fullest one
	ReasonMostDetail = "most_detail" // dates: no agreement, so full date over month over year
	ReasonLargest    = "largest"     // covers: the biggest image
	ReasonFirst      = "first"       // no agreement and no better rule: the first answer
)

// FieldResult is the merged value for one field: the pre-selected value, why
// it was chosen, and the other distinct values. Alternatives is empty when
// every provider that had the field agrees.
type FieldResult struct {
	Value         string   `json:"value"`
	Source        string   `json:"source"`
	SourceDisplay string   `json:"source_display"`
	Reason        string   `json:"reason,omitempty"`
	Sources       []string `json:"sources,omitempty"`
	// Values is the list behind a joined value; set for authors only.
	// Splitting Value on commas breaks a name like "King, Jr.", so a client
	// should read this when it's present.
	Values       []string      `json:"values,omitempty"`
	Alternatives []FieldOption `json:"alternatives"`
}

// CoverOption is a cover URL from a single provider. Width and Height are
// read from the image header when the lookup probes covers; 0 means unknown.
type CoverOption struct {
	Source        string `json:"source"`
	SourceDisplay string `json:"source_display"`
	CoverURL      string `json:"cover_url"`
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
}

// ISBNCandidate is another book a paperback's add-on could stand for.
type ISBNCandidate struct {
	ISBN  string `json:"isbn"`
	Title string `json:"title"`
}

// MergedBookResult is the result of merging every provider's answer.
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
	// Covers lists each distinct cover, pre-selected one first: the largest
	// once sizes are known, else in the order the providers answered.
	Covers      []CoverOption `json:"covers"`
	CoverReason string        `json:"cover_reason,omitempty"`
	// Providers says what each asked provider did, when the lookup reported it.
	Providers []ProviderStatus `json:"providers,omitempty"`
	// FromISBN is the ISBN a UPC lookup was answered by, when the server
	// worked it out from the barcode's add-on rather than trusting the UPC.
	FromISBN string `json:"from_isbn,omitempty"`
	// OtherISBNs are the other books the same add-on could be, when the
	// publisher has more than one ISBN prefix and more than one resolved.
	OtherISBNs []ISBNCandidate `json:"other_isbns,omitempty"`
}

// MergeBookResults combines every provider's answer. There's no priority
// order: each field's pre-selected value comes from the answers themselves
// (see mergeField). results should be in the order the providers answered,
// which is what the registry returns.
func MergeBookResults(results []*BookResult) *MergedBookResult {
	merged := &MergedBookResult{}
	if len(results) == 0 {
		return merged
	}

	catSeen := make(map[string]bool)
	for _, r := range results {
		for _, c := range r.Categories {
			key := strings.ToLower(c)
			if !catSeen[key] {
				catSeen[key] = true
				merged.Categories = append(merged.Categories, c)
			}
		}
	}

	coverSeen := make(map[string]bool)
	for _, r := range results {
		if r.CoverURL != "" && !coverSeen[r.CoverURL] {
			coverSeen[r.CoverURL] = true
			merged.Covers = append(merged.Covers, CoverOption{
				Source:        r.Provider,
				SourceDisplay: r.ProviderDisplay,
				CoverURL:      r.CoverURL,
			})
		}
	}
	if len(merged.Covers) > 0 {
		merged.CoverReason = ReasonFirst
	}

	merged.Title = mergeField(results, func(r *BookResult) string { return r.Title }, byFirst)
	merged.Subtitle = mergeField(results, func(r *BookResult) string { return r.Subtitle }, byFirst)
	merged.Authors = mergeField(results, func(r *BookResult) string { return strings.Join(JoinNameSuffixes(r.Authors), ", ") }, byFirst)
	attachAuthorLists(merged.Authors, results)
	merged.Description = mergeField(results, func(r *BookResult) string { return r.Description }, byLongest)
	merged.Publisher = mergeField(results, func(r *BookResult) string { return r.Publisher }, byFirst)
	merged.PublishDate = mergeField(results, func(r *BookResult) string { return r.PublishDate }, byMostDetail)
	merged.Language = mergeField(results, func(r *BookResult) string { return r.Language }, byFirst)
	merged.ISBN10 = mergeField(results, func(r *BookResult) string { return r.ISBN10 }, byFirst)
	merged.ISBN13 = mergeField(results, func(r *BookResult) string { return r.ISBN13 }, byFirst)
	merged.PageCount = mergeField(results, func(r *BookResult) string {
		if r.PageCount == nil {
			return ""
		}
		return fmt.Sprintf("%d", *r.PageCount)
	}, byFirst)

	return merged
}

// attachAuthorLists gives the merged authors value, and each alternative, the
// list it was joined from, taken from the provider that gave it.
func attachAuthorLists(f *FieldResult, results []*BookResult) {
	if f == nil {
		return
	}
	list := func(provider string) []string {
		for _, r := range results {
			if r.Provider == provider {
				return JoinNameSuffixes(r.Authors)
			}
		}
		return nil
	}
	f.Values = list(f.Source)
	for i := range f.Alternatives {
		f.Alternatives[i].Values = list(f.Alternatives[i].Source)
	}
}

// SortCoversBySize puts the largest known cover first and marks the reason.
// Covers of unknown size keep their answer order after the sized ones.
func (m *MergedBookResult) SortCoversBySize() {
	sized := false
	for _, c := range m.Covers {
		if c.Width > 0 && c.Height > 0 {
			sized = true
			break
		}
	}
	if !sized {
		return
	}
	sort.SliceStable(m.Covers, func(i, j int) bool {
		return m.Covers[i].Width*m.Covers[i].Height > m.Covers[j].Width*m.Covers[j].Height
	})
	m.CoverReason = ReasonLargest
}

// valueGroup is one distinct value of a field and every provider that gave it.
type valueGroup struct {
	value   string // as the first provider wrote it
	sources []*BookResult
}

// mergeField groups the answers by value, ignoring case and spacing. A value
// that two or more providers agree on, more than any other, wins. Otherwise
// tiebreak chooses among the most common values. Returns nil if no provider
// had the field.
func mergeField(results []*BookResult, get func(*BookResult) string, tb tiebreak) *FieldResult {
	var groups []*valueGroup
	index := map[string]*valueGroup{}
	for _, r := range results {
		val := strings.TrimSpace(get(r))
		if val == "" {
			continue
		}
		key := normaliseValue(val)
		g, ok := index[key]
		if !ok {
			g = &valueGroup{value: val}
			index[key] = g
			groups = append(groups, g)
		}
		g.sources = append(g.sources, r)
	}
	if len(groups) == 0 {
		return nil
	}

	best, top := 0, 0
	for _, g := range groups {
		if len(g.sources) > best {
			best, top = len(g.sources), 1
		} else if len(g.sources) == best {
			top++
		}
	}

	var chosen *valueGroup
	reason := ""
	switch {
	case len(groups) == 1 && best == 1:
		chosen, reason = groups[0], ReasonOnly
	case best >= 2 && top == 1:
		for _, g := range groups {
			if len(g.sources) == best {
				chosen = g
			}
		}
		reason = ReasonAgreed
	default:
		var tied []*valueGroup
		for _, g := range groups {
			if len(g.sources) == best {
				tied = append(tied, g)
			}
		}
		chosen, reason = tb.pick(tied), tb.reason
	}

	out := &FieldResult{
		Value:         chosen.value,
		Source:        chosen.sources[0].Provider,
		SourceDisplay: chosen.sources[0].ProviderDisplay,
		Reason:        reason,
		Sources:       providerNames(chosen.sources),
		Alternatives:  []FieldOption{},
	}
	for _, g := range groups {
		if g == chosen {
			continue
		}
		out.Alternatives = append(out.Alternatives, FieldOption{
			Value:         g.value,
			Source:        g.sources[0].Provider,
			SourceDisplay: g.sources[0].ProviderDisplay,
			Sources:       providerNames(g.sources),
		})
	}
	return out
}

// tiebreak picks among equally common values when nothing wins outright.
type tiebreak struct {
	pick   func([]*valueGroup) *valueGroup
	reason string
}

var (
	byFirst      = tiebreak{func(tied []*valueGroup) *valueGroup { return tied[0] }, ReasonFirst}
	byLongest    = tiebreak{pickLongest, ReasonLongest}
	byMostDetail = tiebreak{pickMostDetail, ReasonMostDetail}
)

// pickLongest is for descriptions, where the fuller text is nearly always
// the better one. Ties go to the first answer.
func pickLongest(tied []*valueGroup) *valueGroup {
	best := tied[0]
	for _, g := range tied[1:] {
		if utf8.RuneCountInString(g.value) > utf8.RuneCountInString(best.value) {
			best = g
		}
	}
	return best
}

// pickMostDetail is for dates: a full date beats a month, which beats a year.
func pickMostDetail(tied []*valueGroup) *valueGroup {
	rank := func(v string) int {
		_, p, ok := models.ParseFlexDate(v)
		if !ok {
			return 0
		}
		switch p {
		case models.DatePrecisionDay:
			return 3
		case models.DatePrecisionMonth:
			return 2
		default:
			return 1
		}
	}
	best := tied[0]
	for _, g := range tied[1:] {
		if rank(g.value) > rank(best.value) {
			best = g
		}
	}
	return best
}

func normaliseValue(v string) string {
	return strings.ToLower(strings.Join(strings.Fields(v), " "))
}

func providerNames(rs []*BookResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Provider
	}
	return out
}
