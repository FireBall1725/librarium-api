// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestParseSortKeys(t *testing.T) {
	cases := []struct {
		name, raw, dir string
		want           []SortKey
	}{
		{"empty is no sort", "", "", nil},
		{"legacy single field", "author", "", []SortKey{{Field: SortAuthor}}},
		{"legacy sort_dir applies", "created_at", "desc", []SortKey{{Field: SortCreated, Desc: true}}},
		{"publish_date is year", "publish_date", "", []SortKey{{Field: SortYear}}},
		{"shelf order", "author,series,title", "", []SortKey{
			{Field: SortAuthor}, {Field: SortSeries}, {Field: SortTitle}}},
		{"own direction beats sort_dir", "author-asc,title", "desc", []SortKey{
			{Field: SortAuthor}, {Field: SortTitle, Desc: true}}},
		{"desc and mixed in either order", "series-mixed-desc", "", []SortKey{
			{Field: SortSeries, Desc: true, Mixed: true}}},
		{"mixed only means something on series", "title-mixed", "", []SortKey{{Field: SortTitle}}},
		{"case and spaces", " Author , TITLE-DESC ", "", []SortKey{
			{Field: SortAuthor}, {Field: SortTitle, Desc: true}}},
		{"unknown fields dropped", "colour,title", "", []SortKey{{Field: SortTitle}}},
		{"repeats dropped", "title,title-desc", "", []SortKey{{Field: SortTitle}}},
		{"three levels at most", "author,series,title,year", "", []SortKey{
			{Field: SortAuthor}, {Field: SortSeries}, {Field: SortTitle}}},
		{"nothing usable", "colour,,", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseSortKeys(c.raw, c.dir)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ParseSortKeys(%q, %q) = %+v, want %+v", c.raw, c.dir, got, c.want)
			}
		})
	}
}

func TestBuildSortPlanDefaultsToTitle(t *testing.T) {
	p := buildSortPlan(nil, "", 4, nil)
	if !strings.HasPrefix(p.order, "natural_sort_key(b.title, s_lang.language) ASC") {
		t.Errorf("default order = %q, want title first", p.order)
	}
	if !strings.HasSuffix(p.order, "b.id ASC") {
		t.Errorf("order %q does not end on the id tiebreak", p.order)
	}
	if p.next != 4 || len(p.args) != 0 {
		t.Errorf("no series filter should bind nothing, got next=%d args=%v", p.next, p.args)
	}
}

func TestBuildSortPlanShelfOrder(t *testing.T) {
	keys := []SortKey{{Field: SortAuthor}, {Field: SortSeries}, {Field: SortTitle, Desc: true}}
	p := buildSortPlan(keys, "fr-x-icu", 3, nil)

	for _, want := range []string{"s_lang", "s_auth", "s_ser"} {
		if !strings.Contains(p.join, ") "+want+" ON true") {
			t.Errorf("join is missing %s:\n%s", want, p.join)
		}
	}
	// Author, then standalones after the series, then series name and number,
	// then title backwards, then the tiebreaks.
	wantOrder := []string{
		`lower(s_auth.name) COLLATE "fr-x-icu" ASC NULLS LAST`,
		`(s_ser.id IS NULL) ASC`,
		`natural_sort_key(s_ser.name, s_lang.language) COLLATE "fr-x-icu" ASC`,
		`s_ser.position ASC NULLS LAST`,
		`natural_sort_key(b.title, s_lang.language) COLLATE "fr-x-icu" DESC`,
		`natural_sort_key(b.title, s_lang.language) COLLATE "fr-x-icu" ASC`,
		`b.id ASC`,
	}
	if want := strings.Join(wantOrder, ", "); p.order != want {
		t.Errorf("order =\n%s\nwant\n%s", p.order, want)
	}
}

func TestBuildSortPlanStandalonesStayAfterWhenReversed(t *testing.T) {
	p := buildSortPlan([]SortKey{{Field: SortSeries, Desc: true}}, "", 2, nil)
	if !strings.Contains(p.order, "(s_ser.id IS NULL) ASC, natural_sort_key(s_ser.name, s_lang.language) DESC") {
		t.Errorf("reversing series should not move standalones first: %q", p.order)
	}
}

func TestBuildSortPlanMixedSeries(t *testing.T) {
	p := buildSortPlan([]SortKey{{Field: SortSeries, Mixed: true}}, "", 2, nil)
	if strings.Contains(p.order, "IS NULL") {
		t.Errorf("mixed should not split standalones out: %q", p.order)
	}
	if !strings.Contains(p.order, "COALESCE(s_ser.name, b.title)") {
		t.Errorf("mixed should sort a standalone by its title: %q", p.order)
	}
}

func TestBuildSortPlanPrefersFilteredSeries(t *testing.T) {
	id := uuid.New()
	p := buildSortPlan([]SortKey{{Field: SortSeries}}, "", 5, []uuid.UUID{id})
	if !strings.Contains(p.join, "(bs.series_id = ANY($5)) DESC") {
		t.Errorf("series join should prefer the filtered series via $5:\n%s", p.join)
	}
	if p.next != 6 || len(p.args) != 1 {
		t.Errorf("want one bound arg and next=6, got next=%d args=%v", p.next, p.args)
	}
}

func TestBuildSortPlanDatesGoLastWhenMissing(t *testing.T) {
	p := buildSortPlan([]SortKey{{Field: SortAdded, Desc: true}, {Field: SortYear}}, "", 2, nil)
	if strings.Count(p.order, "NULLS LAST") != 2 {
		t.Errorf("added and year should both put missing dates last: %q", p.order)
	}
	if !strings.Contains(p.order, "cp.library_id = ANY($1)") {
		t.Errorf("added should only count copies in the caller's libraries: %q", p.order)
	}
}

func TestSortCollationRejectsUnknownLanguages(t *testing.T) {
	r := &BookRepo{}
	// Unknown before any lookup, so no database is touched.
	for _, lang := range []string{"", "xx", "klingon", `en"; DROP TABLE books; --`} {
		if got := r.sortCollation(lang); got != "" {
			t.Errorf("sortCollation(%q) = %q, want empty", lang, got)
		}
	}
}
