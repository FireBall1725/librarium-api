// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"fmt"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
)

// FacetValue is one selectable value in a facet, with the number of books that
// would match if it were chosen.
type FacetValue struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// BookFacets is the counts block returned alongside a book search, so a client
// can render a filter rail without a request per value.
type BookFacets struct {
	ReadStatus []FacetValue `json:"read_status"`
	MediaType  []FacetValue `json:"media_type"`
	Genre      []FacetValue `json:"genre"`
	Tag        []FacetValue `json:"tag"`
	Rating     []FacetValue `json:"rating"`
}

// Facets counts books per facet value over the whole filtered set, not the
// page. Everything is one round trip: a query per facet would multiply with the
// number of dimensions and each would have to rebuild the same filter.
//
// Read status and rating come from user_book_interactions, which is keyed on
// book_edition_id rather than book_id. Joining editions directly would count a
// work with three editions three times, so interactions are collapsed to one
// row per book first. Measured during the spike: 1,000 extra editions inflated
// the status counts by exactly 1,000 before this was fixed, and fixing it cost
// nothing (338ms against 340ms at 100k books).
//
// Best status wins, read > reading > did_not_finish > unread, because "have I
// read this" is a question about the work rather than about one printing.
// libraryIDs scopes the counts. A single entry serves the per-library route;
// the caller's whole membership set serves the cross-library Books surface.
func (r *BookRepo) Facets(ctx context.Context, libraryIDs []uuid.UUID, opts ListBooksOpts) (*BookFacets, error) {
	if len(libraryIDs) == 0 {
		// No accessible libraries is a legitimate state, not an error: a new
		// user, or one whose only membership was revoked. Empty facets let the
		// client render its own empty state instead of a failure.
		return &BookFacets{
			ReadStatus: []FacetValue{}, MediaType: []FacetValue{}, Genre: []FacetValue{},
			Tag: []FacetValue{}, Rating: []FacetValue{},
		}, nil
	}
	f := r.buildBookFilter(libraryIDs, opts)

	args := f.args
	callerArg := 0
	if opts.CallerID != uuid.Nil {
		args = append(args, opts.CallerID)
		callerArg = f.nextArg
	}

	// The filtered set of book ids, computed once and reused by every facet.
	interJoin := "SELECT NULL::uuid AS book_id, NULL::text AS read_status, NULL::int AS rating WHERE false"
	if callerArg > 0 {
		interJoin = fmt.Sprintf(`
			SELECT e.book_id,
			       CASE min(CASE i.read_status
			                    WHEN 'read' THEN 1 WHEN 'reading' THEN 2
			                    WHEN 'did_not_finish' THEN 3 ELSE 4 END)
			            WHEN 1 THEN 'read' WHEN 2 THEN 'reading'
			            WHEN 3 THEN 'did_not_finish' ELSE 'unread' END AS read_status,
			       max(i.rating) AS rating
			FROM user_book_interactions i
			JOIN book_editions e ON e.id = i.book_edition_id
			WHERE i.user_id = $%d AND i.deleted_at IS NULL
			GROUP BY e.book_id`, callerArg)
	}

	query := fmt.Sprintf(`
WITH scope AS (
    SELECT DISTINCT b.id, b.media_type_id
    FROM books b
    JOIN media_types mt ON mt.id = b.media_type_id
    %s
    %s
),
inter AS (%s),
joined AS (
    SELECT s.id, s.media_type_id,
           COALESCE(x.read_status, 'unread') AS read_status,
           x.rating
    FROM scope s
    LEFT JOIN inter x ON x.book_id = s.id
)
SELECT 'read_status' AS dim, read_status AS value, read_status AS label, count(*) AS n
FROM joined GROUP BY 2, 3
UNION ALL
SELECT 'media_type', mt.name, mt.display_name, count(*)
FROM joined j JOIN media_types mt ON mt.id = j.media_type_id GROUP BY 2, 3
UNION ALL
SELECT 'genre', g.name, g.name, count(*)
FROM joined j JOIN book_genres bg ON bg.book_id = j.id JOIN genres g ON g.id = bg.genre_id
GROUP BY 2, 3
UNION ALL
SELECT 'tag', t.name, t.name, count(*)
FROM joined j JOIN book_tags bt ON bt.book_id = j.id AND bt.deleted_at IS NULL
              JOIN tags t ON t.id = bt.tag_id AND t.deleted_at IS NULL
GROUP BY 2, 3
UNION ALL
SELECT 'rating', rating::text, rating::text, count(*)
FROM joined WHERE rating IS NOT NULL GROUP BY 2, 3
ORDER BY 1, 4 DESC, 2`, f.scopeJoin, f.where, interJoin)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("counting book facets: %w", err)
	}
	defer rows.Close()

	out := &BookFacets{
		ReadStatus: []FacetValue{},
		MediaType:  []FacetValue{},
		Genre:      []FacetValue{},
		Tag:        []FacetValue{},
		Rating:     []FacetValue{},
	}
	for rows.Next() {
		var dim string
		var fv FacetValue
		if err := rows.Scan(&dim, &fv.Value, &fv.Label, &fv.Count); err != nil {
			return nil, fmt.Errorf("scanning facet row: %w", err)
		}
		switch dim {
		case "read_status":
			out.ReadStatus = append(out.ReadStatus, fv)
		case "media_type":
			out.MediaType = append(out.MediaType, fv)
		case "genre":
			out.Genre = append(out.Genre, fv)
		case "tag":
			out.Tag = append(out.Tag, fv)
		case "rating":
			out.Rating = append(out.Rating, fv)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading facet rows: %w", err)
	}
	return out, nil
}

// LibraryFacet counts the filtered books per library.
//
// Kept out of Facets because it only means anything across several libraries:
// on a per-library route it would return one value whose count equals the
// total, which is noise rather than a filter.
func (r *BookRepo) LibraryFacet(ctx context.Context, libraryIDs []uuid.UUID, opts ListBooksOpts) ([]FacetValue, error) {
	if len(libraryIDs) == 0 {
		return []FacetValue{}, nil
	}
	f := r.buildBookFilter(libraryIDs, opts)

	// COUNT(DISTINCT b.id): a work held by two libraries is one book in each,
	// not two, and the junction would otherwise multiply it.
	// Unlike every other facet this one wants a row per (book, library), so it
	// joins the junction directly instead of the deduping scope join and
	// reapplies the library predicate itself. COUNT(DISTINCT b.id) still
	// guards against a book appearing twice within one library.
	query := fmt.Sprintf(`
		SELECT l.id::text, l.name, COUNT(DISTINCT b.id)
		FROM books b
		JOIN media_types mt ON mt.id = b.media_type_id
		JOIN library_books lbx ON lbx.book_id = b.id AND lbx.deleted_at IS NULL
		JOIN libraries l ON l.id = lbx.library_id
		%s
		AND lbx.library_id = ANY($1)
		GROUP BY l.id, l.name
		ORDER BY 3 DESC, 2`, f.where)

	rows, err := r.db.Query(ctx, query, f.args...)
	if err != nil {
		return nil, fmt.Errorf("counting library facet: %w", err)
	}
	defer rows.Close()

	out := []FacetValue{}
	for rows.Next() {
		var fv FacetValue
		if err := rows.Scan(&fv.Value, &fv.Label, &fv.Count); err != nil {
			return nil, fmt.Errorf("scanning library facet: %w", err)
		}
		out = append(out, fv)
	}
	return out, rows.Err()
}

// ListAcross lists books spanning several libraries, deduplicated: a work held
// by two libraries appears once, not twice.
func (r *BookRepo) ListAcross(ctx context.Context, libraryIDs []uuid.UUID, opts ListBooksOpts) ([]*models.Book, int, error) {
	if len(libraryIDs) == 0 {
		return []*models.Book{}, 0, nil
	}
	return r.listScoped(ctx, libraryIDs, opts)
}
