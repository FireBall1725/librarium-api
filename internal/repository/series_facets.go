// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// SeriesFacets counts series per filter value, the same contract BookFacets
// has: counts describe the whole filtered set rather than a page, and each
// dimension is counted with its own selection EXCLUDED.
//
// That exclusion is the whole point of a rail. Applying every filter uniformly
// collapses the dimension you just used: tick Ongoing and the status list shows
// only Ongoing, so adding Complete becomes impossible without clearing first.
// Counting around a dimension's own selection answers the question the rail is
// actually asking, which is "what would I get if I picked this one as well".
type SeriesFacets struct {
	Library []FacetValue `json:"library"`
	// MediaType is what kind of thing the run is, read off the books in it
	// rather than stored on the series. Nothing says a series is manga; every
	// book in it says so, and no series in a real collection mixes, so the
	// answer is already there and only needed asking for.
	MediaType []FacetValue `json:"media_type"`
	// Genre is the series' own list, which providers fill in. Free text rather
	// than the controlled vocabulary book genres use, so the counts here show
	// the vocabulary as it actually is, spellings and all. That is the honest
	// way round: a facet that reports "Sci-Fi 4" beside "Science fiction 3" is
	// how anyone finds out the two need merging.
	Genre  []FacetValue `json:"genre"`
	Status []FacetValue `json:"status"`
	// Arcs is a shape rather than a value: "with" and "without" are the two
	// answers, counted so a reader can see there is nothing behind one of them.
	Arcs []FacetValue `json:"arcs"`
	// Reading is how far the caller is through each run, derived rather than
	// stored. Empty for an anonymous read, which has no progress to describe.
	Reading []FacetValue `json:"reading"`
	Tag     []FacetValue `json:"tag"`
}

func emptySeriesFacets() *SeriesFacets {
	return &SeriesFacets{
		Library: []FacetValue{}, MediaType: []FacetValue{}, Genre: []FacetValue{},
		Status: []FacetValue{}, Arcs: []FacetValue{}, Reading: []FacetValue{},
		Tag: []FacetValue{},
	}
}

// Facets counts the series index.
//
// One pass, not a query per dimension: the match flags are computed once per
// row and each dimension aggregates using the flags it does not own. Text
// search is not a facet, so it narrows every dimension including the one being
// counted, and sits in the base scope with the library-readable check.
func (r *SeriesRepo) Facets(
	ctx context.Context,
	accessible []uuid.UUID,
	callerID uuid.UUID,
	search string,
	f SeriesFilter,
) (*SeriesFacets, error) {
	if len(accessible) == 0 {
		return emptySeriesFacets(), nil
	}

	args := []any{accessible}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	textWhere := ""
	if search != "" {
		textWhere = " AND lower(s.name) LIKE lower(" + arg("%"+search+"%") + ")"
	}

	// Read state needs the caller. An anonymous read has no progress to
	// describe, so the counts are computed against a row that matches nothing
	// rather than the dimension being omitted and the client finding a hole.
	readExpr, readingExpr := "0", "0"
	if callerID != uuid.Nil {
		p := arg(callerID)
		n := len(args)
		_ = p
		readExpr = seriesReadCountExpr(n)
		readingExpr = seriesReadingCountExpr(n)
	}

	// Every dimension's own condition, so each aggregate can use the others.
	// An unselected dimension contributes TRUE rather than being skipped, which
	// keeps the format arguments below positional and countable.
	libCond, statusCond, arcsCond, readingCond, tagCond := "TRUE", "TRUE", "TRUE", "TRUE", "TRUE"
	mediaCond, genreCond := "TRUE", "TRUE"
	if len(f.Libraries) > 0 {
		libCond = "f.library_id = ANY(" + arg(f.Libraries) + ")"
	}
	if len(f.MediaTypes) > 0 {
		mediaCond = seriesMediaTypeExists("f.id", arg(f.MediaTypes))
	}
	if len(f.Genres) > 0 {
		// Ticking two genres means either, which is what a checkbox list means
		// everywhere else in the rail.
		genreCond = `EXISTS (
            SELECT 1 FROM series_genres sg_f JOIN genres g_f ON g_f.id = sg_f.genre_id
             WHERE sg_f.series_id = f.id AND g_f.name = ANY(` + arg(f.Genres) + `))`
	}
	if f.Status != "" {
		statusCond = "f.status = " + arg(f.Status)
	}
	switch f.Arcs {
	case "with":
		arcsCond = "f.arc_count > 0"
	case "without":
		arcsCond = "f.arc_count = 0"
	}
	if callerID != uuid.Nil {
		switch f.Reading {
		case "unread":
			readingCond = "f.read_count = 0 AND f.reading_count = 0"
		case "reading":
			readingCond = "f.reading_count > 0"
		case "read_all":
			readingCond = "f.book_count > 0 AND f.read_count >= f.book_count"
		}
	}
	if f.Tag != "" {
		tagCond = fmt.Sprintf(`EXISTS (
            SELECT 1 FROM series_tags st_f JOIN tags t_f ON t_f.id = st_f.tag_id
             WHERE st_f.series_id = f.id AND lower(t_f.name) = lower(%s))`, arg(f.Tag))
	}

	// Every dimension except the one being counted.
	without := func(skip string) string {
		parts := make([]string, 0, 4)
		for name, cond := range map[string]string{
			"library": libCond, "media_type": mediaCond, "genre": genreCond,
			"status": statusCond, "arcs": arcsCond,
			"reading": readingCond, "tag": tagCond,
		} {
			if name != skip && cond != "TRUE" {
				parts = append(parts, cond)
			}
		}
		if len(parts) == 0 {
			return "TRUE"
		}
		// Sorted so the same selection always produces the same SQL, which is
		// what lets Postgres reuse a plan across requests. Ranging a map is
		// otherwise ordered differently every call.
		sort := make([]string, len(parts))
		copy(sort, parts)
		for i := 0; i < len(sort); i++ {
			for j := i + 1; j < len(sort); j++ {
				if sort[j] < sort[i] {
					sort[i], sort[j] = sort[j], sort[i]
				}
			}
		}
		return strings.Join(sort, " AND ")
	}

	q := fmt.Sprintf(`
WITH f AS (
    SELECT s.id, s.library_id, s.status,
           (SELECT COUNT(*) FROM series_arcs sa WHERE sa.series_id = s.id) AS arc_count,
           %s AS book_count,
           %s AS read_count,
           %s AS reading_count
      FROM series s
     WHERE s.library_id = ANY($1)%s
)
SELECT 'library' AS dim, l.id::text AS value, l.name AS label, COUNT(*) AS n
  FROM f JOIN libraries l ON l.id = f.library_id
 WHERE %s GROUP BY l.id, l.name
UNION ALL
SELECT 'media_type', mt.name, mt.display_name, COUNT(DISTINCT f.id)
  FROM f JOIN book_series bs_m ON bs_m.series_id = f.id
         JOIN books b_m ON b_m.id = bs_m.book_id
         JOIN media_types mt ON mt.id = b_m.media_type_id
 WHERE %s GROUP BY mt.name, mt.display_name
UNION ALL
SELECT 'genre', g.name, g.name, COUNT(DISTINCT f.id)
  FROM f JOIN series_genres sg ON sg.series_id = f.id
         JOIN genres g ON g.id = sg.genre_id
 WHERE %s GROUP BY g.name
UNION ALL
SELECT 'status', f.status, f.status, COUNT(*)
  FROM f WHERE %s GROUP BY f.status
UNION ALL
SELECT 'arcs', CASE WHEN f.arc_count > 0 THEN 'with' ELSE 'without' END,
       CASE WHEN f.arc_count > 0 THEN 'with' ELSE 'without' END, COUNT(*)
  FROM f WHERE %s GROUP BY 2, 3
UNION ALL
SELECT 'reading',
       CASE WHEN f.book_count > 0 AND f.read_count >= f.book_count THEN 'read_all'
            WHEN f.reading_count > 0 OR f.read_count > 0 THEN 'reading'
            ELSE 'unread' END,
       CASE WHEN f.book_count > 0 AND f.read_count >= f.book_count THEN 'read_all'
            WHEN f.reading_count > 0 OR f.read_count > 0 THEN 'reading'
            ELSE 'unread' END,
       COUNT(*)
  FROM f WHERE %s GROUP BY 2, 3
UNION ALL
SELECT 'tag', t.name, t.name, COUNT(DISTINCT f.id)
  FROM f JOIN series_tags st ON st.series_id = f.id
         JOIN tags t ON t.id = st.tag_id AND t.deleted_at IS NULL
 WHERE %s GROUP BY t.name`,
		seriesBookCountValue, readExpr, readingExpr, textWhere,
		without("library"), without("media_type"), without("genre"),
		without("status"), without("arcs"), without("reading"), without("tag"))

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("counting series facets: %w", err)
	}
	defer rows.Close()

	out := emptySeriesFacets()
	for rows.Next() {
		var dim, value, label string
		var n int
		if err := rows.Scan(&dim, &value, &label, &n); err != nil {
			return nil, err
		}
		v := FacetValue{Value: value, Label: label, Count: n}
		switch dim {
		case "library":
			out.Library = append(out.Library, v)
		case "media_type":
			out.MediaType = append(out.MediaType, v)
		case "genre":
			out.Genre = append(out.Genre, v)
		case "status":
			out.Status = append(out.Status, v)
		case "arcs":
			out.Arcs = append(out.Arcs, v)
		case "reading":
			// Anonymous readers get no progress, so every row would report
			// unread and the dimension would say something untrue rather than
			// nothing. Dropped instead.
			if callerID != uuid.Nil {
				out.Reading = append(out.Reading, v)
			}
		case "tag":
			out.Tag = append(out.Tag, v)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Biggest first for the open vocabularies, so the rows worth clicking are
	// at the top of a list that can run long.
	byCountDesc(out.Library)
	byCountDesc(out.MediaType)
	byCountDesc(out.Genre)
	byCountDesc(out.Tag)
	// Status, arcs and reading are closed vocabularies with a natural order, so
	// they keep it: a rail whose rows reshuffle as counts change is one the
	// reader has to re-read every time they click.
	orderBy(&out.Status, []string{"ongoing", "completed", "hiatus", "cancelled"})
	orderBy(&out.Arcs, []string{"with", "without"})
	orderBy(&out.Reading, []string{"unread", "reading", "read_all"})
	return out, nil
}

// orderBy puts values in a fixed order, dropping nothing: anything not named
// keeps its relative position at the end, so an unexpected status still shows.
func orderBy(values *[]FacetValue, order []string) {
	rank := make(map[string]int, len(order))
	for i, v := range order {
		rank[v] = i
	}
	at := func(v FacetValue) int {
		if i, ok := rank[v.Value]; ok {
			return i
		}
		return len(order)
	}
	vs := *values
	for i := 0; i < len(vs); i++ {
		for j := i + 1; j < len(vs); j++ {
			if at(vs[j]) < at(vs[i]) {
				vs[i], vs[j] = vs[j], vs[i]
			}
		}
	}
}

// byCountDesc puts the largest rows first, name breaking a tie so the order is
// stable across reloads when two values match the same number of series.
func byCountDesc(vs []FacetValue) {
	sort.SliceStable(vs, func(i, j int) bool {
		if vs[i].Count != vs[j].Count {
			return vs[i].Count > vs[j].Count
		}
		return strings.ToLower(vs[i].Label) < strings.ToLower(vs[j].Label)
	})
}

// seriesMediaTypeExists asks whether a series holds a book of one of these
// kinds.
//
// Any book, not every book: nothing stops a run mixing a novel with its manga
// adaptation, and a reader ticking Manga means "show me the manga" rather than
// "show me runs that are nothing but manga".
func seriesMediaTypeExists(seriesRef, placeholder string) string {
	return fmt.Sprintf(`EXISTS (
        SELECT 1 FROM book_series bs_f
          JOIN books b_f ON b_f.id = bs_f.book_id
          JOIN media_types mt_f ON mt_f.id = b_f.media_type_id
         WHERE bs_f.series_id = %s AND mt_f.name = ANY(%s))`, seriesRef, placeholder)
}
