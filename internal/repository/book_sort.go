// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725
//
// Sorting the book list by up to three things at once.
//
// "By author" alone leaves each author's books in whatever order the database
// finds them, which is not how anyone shelves. Author, then series, then title
// is: each author's runs in reading order, then their standalone books. So the
// sort is a short list of keys, each with its own direction, and the wire form
// is that list written out: sort=author,series,title-desc.
//
// Parsing never fails. An unknown field, a repeat, or a fourth level is
// dropped, the way the rest of the list parameters ignore what they do not
// understand, so an old or mistyped link still returns a list rather than a
// 400. The single-field values iOS and the per-library page already send
// (title, author, created_at, publish_date, media_type, with sort_dir) parse
// into a one-key list and sort the way they did.

package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SortField is one thing the book list can be ordered by.
type SortField string

const (
	SortTitle SortField = "title"
	// SortAuthor is the first credited author's sort name, "Adams, Douglas".
	SortAuthor SortField = "author"
	// SortSeries is the series name, then the book's number in it.
	SortSeries SortField = "series"
	// SortAdded is when a copy first arrived in one of the caller's libraries.
	SortAdded SortField = "added"
	// SortYear is the primary edition's publish date.
	SortYear SortField = "year"
	// SortCreated is when the work entered the catalogue, which for a book
	// another library already held can be months before this one got it.
	// Kept because iOS sends it for "Recently added"; SortAdded is the answer
	// to that question.
	SortCreated   SortField = "created_at"
	SortMediaType SortField = "media_type"
)

// MaxSortKeys is how many levels a sort may have. Author, series, title is
// three, and nothing on a shelf has needed a fourth.
const MaxSortKeys = 3

// SortKey is one level of a sort.
type SortKey struct {
	Field SortField
	Desc  bool
	// Mixed applies to SortSeries only. Off, books in no series come after the
	// ones in a series, whichever way the level runs. On, a standalone book
	// sorts by its own title among the series names.
	Mixed bool
}

var sortFieldNames = map[string]SortField{
	"title":        SortTitle,
	"author":       SortAuthor,
	"series":       SortSeries,
	"added":        SortAdded,
	"year":         SortYear,
	"publish_date": SortYear,
	"created_at":   SortCreated,
	"media_type":   SortMediaType,
}

// ParseSortKeys reads the sort parameter. legacyDir is the old sort_dir,
// applied to any level that does not say its own direction, which is how a
// single-field sort from an older client keeps working.
func ParseSortKeys(raw, legacyDir string) []SortKey {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	legacyDesc := strings.EqualFold(strings.TrimSpace(legacyDir), "desc")

	var keys []SortKey
	seen := map[SortField]bool{}
	for _, tok := range strings.Split(raw, ",") {
		parts := strings.Split(strings.ToLower(strings.TrimSpace(tok)), "-")
		field, ok := sortFieldNames[parts[0]]
		if !ok || seen[field] {
			continue
		}
		k := SortKey{Field: field, Desc: legacyDesc}
		for _, flag := range parts[1:] {
			switch flag {
			case "desc":
				k.Desc = true
			case "asc":
				k.Desc = false
			case "mixed":
				k.Mixed = field == SortSeries
			}
		}
		seen[field] = true
		keys = append(keys, k)
		if len(keys) == MaxSortKeys {
			break
		}
	}
	return keys
}

// SeriesOrder is the sort a list filtered to one series gets when the caller
// did not pick one: reading order, which is the question being asked.
func SeriesOrder() []SortKey {
	return []SortKey{{Field: SortSeries}, {Field: SortTitle}}
}

// sortCollations maps a reader's language to the ICU collation that orders
// text the way that language does: Å with A in English, after Z in Swedish.
// A fixed list rather than whatever the client sends, because a collation name
// is spliced into the SQL and cannot be a bind parameter.
var sortCollations = map[string]string{
	"en": "en-x-icu", "fr": "fr-x-icu", "de": "de-x-icu", "es": "es-x-icu",
	"pt": "pt-x-icu", "it": "it-x-icu", "nl": "nl-x-icu", "ja": "ja-x-icu",
	"sv": "sv-x-icu", "da": "da-x-icu", "nb": "nb-x-icu", "no": "nb-x-icu",
	"fi": "fi-x-icu", "pl": "pl-x-icu", "cs": "cs-x-icu", "tr": "tr-x-icu",
	"ru": "ru-x-icu", "uk": "uk-x-icu", "he": "he-x-icu", "zh": "zh-x-icu",
	"ko": "ko-x-icu",
}

// sortCollation returns the collation for a reader's language tag ("fr-FR",
// "sv"), or "" to use the database default. A Postgres built without ICU has
// none of these, so the names are checked against pg_collation once and a
// missing one falls back rather than failing every list request.
func (r *BookRepo) sortCollation(lang string) string {
	name := sortCollations[strings.ToLower(strings.SplitN(strings.TrimSpace(lang), "-", 2)[0])]
	if name == "" {
		return ""
	}
	r.collOnce.Do(func() {
		r.colls = map[string]bool{}
		// Not the request's context: a cancelled first request would leave the
		// set empty for the life of the process.
		qctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rows, err := r.db.Query(qctx, `SELECT collname FROM pg_collation WHERE collprovider = 'i'`)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil {
				r.colls[n] = true
			}
		}
	})
	if !r.colls[name] {
		return ""
	}
	return name
}

// sortPlan is the SQL a sort adds to a book query: the lateral joins its keys
// read from, the ORDER BY terms, and the args those bind.
type sortPlan struct {
	join  string
	order string
	args  []any
	next  int
}

// buildSortPlan turns keys into SQL for a query over `books b` joined to
// `media_types mt`, with the caller's library scope bound as $1.
//
// seriesIDs is the series filter, if any. A book in two series sorts by the one
// being looked at, so Robot Series filtered shows Robot Series numbers even for
// a book that is also in Foundation.
//
// Every plan ends on title then id, so books that tie on every chosen key still
// come back in the same order on every page.
func buildSortPlan(keys []SortKey, collation string, argIdx int, seriesIDs []uuid.UUID) sortPlan {
	if len(keys) == 0 {
		keys = []SortKey{{Field: SortTitle}}
	}
	p := sortPlan{next: argIdx}
	coll := ""
	if collation != "" {
		coll = fmt.Sprintf(` COLLATE "%s"`, collation)
	}
	dir := func(k SortKey) string {
		if k.Desc {
			return "DESC"
		}
		return "ASC"
	}

	// The primary edition's language decides which articles a title drops.
	joins := []string{`LEFT JOIN LATERAL (
		SELECT be.language FROM book_editions be
		WHERE be.book_id = b.id AND be.is_primary
		LIMIT 1
	) s_lang ON true`}
	titleKey := "natural_sort_key(b.title, s_lang.language)" + coll

	var terms []string
	for _, k := range keys {
		switch k.Field {
		case SortTitle:
			terms = append(terms, titleKey+" "+dir(k))

		case SortAuthor:
			// The first credited author, falling back to the first contributor
			// of any role for a book with no author credit, such as an art book.
			joins = append(joins, `LEFT JOIN LATERAL (
		SELECT COALESCE(NULLIF(c.sort_name, ''), c.name) AS name
		FROM book_contributors bc
		JOIN contributors c ON c.id = bc.contributor_id
		WHERE bc.book_id = b.id
		ORDER BY (bc.role = 'author') DESC, bc.display_order, c.name
		LIMIT 1
	) s_auth ON true`)
			// A book with no contributor is last either way; "no author" is not
			// a name that sorts before Aaronovitch.
			terms = append(terms, "lower(s_auth.name)"+coll+" "+dir(k)+" NULLS LAST")

		case SortSeries:
			pick := ""
			if len(seriesIDs) > 0 {
				pick = fmt.Sprintf("(bs.series_id = ANY($%d)) DESC, ", p.next)
				p.args = append(p.args, seriesIDs)
				p.next++
			}
			// Series are per library, so only ones in the caller's scope count.
			joins = append(joins, `LEFT JOIN LATERAL (
		SELECT s.id, s.name, bs.position
		FROM book_series bs
		JOIN series s ON s.id = bs.series_id
		WHERE bs.book_id = b.id AND s.library_id = ANY($1)
		ORDER BY `+pick+`lower(s.name), s.id
		LIMIT 1
	) s_ser ON true`)
			if k.Mixed {
				terms = append(terms,
					"natural_sort_key(COALESCE(s_ser.name, b.title), s_lang.language)"+coll+" "+dir(k),
					"COALESCE(s_ser.position, 0) "+dir(k))
			} else {
				// Not reversed by DESC: standalones follow the series either way.
				terms = append(terms,
					"(s_ser.id IS NULL) ASC",
					"natural_sort_key(s_ser.name, s_lang.language)"+coll+" "+dir(k),
					"s_ser.position "+dir(k)+" NULLS LAST")
			}

		case SortAdded:
			// A wishlisted or suggested book has no copy, so no date, and goes last.
			terms = append(terms, `(
		SELECT min(cp.created_at) FROM copies cp
		WHERE cp.book_id = b.id AND cp.library_id = ANY($1) AND cp.deleted_at IS NULL
	) `+dir(k)+" NULLS LAST")

		case SortYear:
			terms = append(terms, `(
		SELECT be.publish_date FROM book_editions be
		WHERE be.book_id = b.id AND be.is_primary = true AND be.publish_date IS NOT NULL
		LIMIT 1
	) `+dir(k)+" NULLS LAST")

		case SortCreated:
			terms = append(terms, "b.created_at "+dir(k))

		case SortMediaType:
			terms = append(terms, "lower(mt.display_name)"+coll+" "+dir(k))
		}
	}
	terms = append(terms, titleKey+" ASC", "b.id ASC")

	p.join = " " + strings.Join(joins, " ") + " "
	p.order = strings.Join(terms, ", ")
	return p
}
