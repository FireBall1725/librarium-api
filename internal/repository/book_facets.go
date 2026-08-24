// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"fmt"
	"strings"

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
	Ownership  []FacetValue `json:"ownership"`
	Library    []FacetValue `json:"library"`
	ReadStatus []FacetValue `json:"read_status"`
	MediaType  []FacetValue `json:"media_type"`
	Genre      []FacetValue `json:"genre"`
	Tag        []FacetValue `json:"tag"`
	Shelf      []FacetValue `json:"shelf"`
	Rating     []FacetValue `json:"rating"`
	// Favourite is the per-book starred flag, counted as a dimension so
	// "Favourites" can be a saved view with a number beside it. A rule, not a
	// hand-picked set, which is what separates it from a shelf.
	Favourite []FacetValue `json:"favourite"`
}

// FacetSelection is what the user has ticked in each facet. An empty slice
// means that facet is not filtering.
//
// Passed as structured values rather than as opaque filter JSON because each
// dimension has to be counted with its OWN selection removed, which is
// impossible once the conditions have been flattened into one WHERE string.
type FacetSelection struct {
	// Ownership is where a book stands in relation to the caller: on the
	// shelf, wishlisted, suggested, or a gap in a series. See book_ownership.go.
	Ownership  []string
	Libraries  []uuid.UUID
	ReadStatus []string
	MediaTypes []string // media_types.name
	Genres     []string
	Tags       []string
	// Shelves are hand-picked sets rather than a property of a book, but they
	// narrow the list exactly like a tag does, so they are a facet dimension.
	Shelves []uuid.UUID
	Ratings []int32
	// Favourites filters on the starred flag. A slice rather than a *bool so
	// it behaves like every other dimension: empty means not filtering.
	Favourites []bool
}

func emptyFacets() *BookFacets {
	return &BookFacets{
		Ownership: []FacetValue{},
		Library:   []FacetValue{}, ReadStatus: []FacetValue{}, MediaType: []FacetValue{},
		Genre: []FacetValue{}, Tag: []FacetValue{}, Shelf: []FacetValue{},
		Rating: []FacetValue{}, Favourite: []FacetValue{},
	}
}

// Facets counts books per facet value over the whole filtered set, not the page.
//
// Each dimension is counted with its own selection EXCLUDED. Applying every
// filter uniformly collapses the facet you just used: tick Fantasy and the
// genre list shows only Fantasy, so adding Science Fiction becomes impossible
// without clearing first. Excluding a dimension's own selection answers the
// question the rail is actually asking, which is "what would I get if I picked
// this one as well".
//
// One pass, not a query per dimension. Match flags are computed once per row and
// each dimension aggregates using the five flags it does not own.
//
// Read status and rating come from user_books, which is keyed on
// book_edition_id. Joining editions directly counts a work with three editions
// three times, so interactions collapse to one row per book first, best status
// winning: read > reading > did_not_finish > unread.
func (r *BookRepo) Facets(
	ctx context.Context,
	accessible []uuid.UUID,
	sel FacetSelection,
	query string,
	seriesIDs []uuid.UUID,
	callerID uuid.UUID,
) (*BookFacets, error) {
	if len(accessible) == 0 {
		return emptyFacets(), nil
	}

	args := []any{accessible}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	// Scope first, because it binds the caller's own placeholder and the
	// numbering follows the order arg() is called in, not the order the pieces
	// appear in the format string.
	//
	// The caller is bound once here and again for the interaction CTE below.
	// Two binds of the same value is cheaper than threading one placeholder
	// through both and getting the numbering wrong.
	scopeCallerArg := 0
	if callerID != uuid.Nil {
		arg(callerID)
		scopeCallerArg = len(args)
	}
	scopeSQL := bookScopeCTE(1, scopeCallerArg)

	// Text search is not a facet, so it narrows every dimension including the
	// one being counted, and belongs in the base scope.
	textWhere := ""
	if query != "" {
		p := arg("%" + query + "%")
		textWhere = fmt.Sprintf(`AND (b.title ILIKE %[1]s OR EXISTS (
            SELECT 1 FROM book_contributors bc JOIN contributors c ON c.id = bc.contributor_id
            WHERE bc.book_id = b.id AND c.name ILIKE %[1]s))`, p)
	}

	// Drilling into a series narrows every dimension, for the same reason the
	// text search does: it is not one of the facets, so no dimension owns it and
	// none may count around it. Without this the rail reported the whole shelf
	// beside a list showing one run.
	if len(seriesIDs) > 0 {
		p := arg(seriesIDs)
		textWhere += fmt.Sprintf(` AND EXISTS (
            SELECT 1 FROM book_series bs_f WHERE bs_f.book_id = b.id AND bs_f.series_id = ANY(%s))`, p)
	}

	interCTE := "SELECT NULL::uuid AS book_id, NULL::text AS read_status, NULL::int AS rating, NULL::bool AS is_favorite WHERE false"
	if callerID != uuid.Nil {
		p := arg(callerID)
		// One row per work already, so no GROUP BY and no collapsing. The
		// aggregate this replaced was not a summary of anything: max(rating)
		// and bool_or(is_favorite) existed only to reduce several editions to
		// one answer, and reducing them is what made starring a paperback star
		// the hardcover.
		interCTE = fmt.Sprintf(`
            SELECT ub.book_id, ub.read_status, ub.rating, ub.is_favorite
            FROM user_books ub
            WHERE ub.user_id = %s AND ub.deleted_at IS NULL`, p)
	}

	// One flag per dimension. An unused facet is always TRUE so it narrows
	// nothing, and critically its parameter is never bound: Go evaluates call
	// arguments eagerly, so building the expression unconditionally would append
	// an arg whose placeholder never reaches the SQL, and Postgres then fails
	// with "could not determine data type of parameter $N".
	mOwn := "TRUE"
	mLib, mStatus, mType := "TRUE", "TRUE", "TRUE"
	mGenre, mTag, mRating := "TRUE", "TRUE", "TRUE"
	mShelf := "TRUE"
	mFav := "TRUE"

	if len(sel.Ownership) > 0 {
		mOwn = fmt.Sprintf("s.ownership = ANY(%s)", arg(sel.Ownership))
	}

	if len(sel.Libraries) > 0 {
		mLib = fmt.Sprintf(`EXISTS (SELECT 1 FROM held_books lb2 WHERE lb2.book_id = s.id
                 AND lb2.deleted_at IS NULL AND lb2.library_id = ANY(%s))`, arg(sel.Libraries))
	}
	if len(sel.ReadStatus) > 0 {
		mStatus = fmt.Sprintf(`COALESCE(x.read_status, 'unread') = ANY(%s)`, arg(sel.ReadStatus))
	}
	if len(sel.MediaTypes) > 0 {
		mType = fmt.Sprintf(
			`EXISTS (SELECT 1 FROM media_types mt2 WHERE mt2.id = s.media_type_id AND mt2.name = ANY(%s))`,
			arg(sel.MediaTypes))
	}
	if len(sel.Genres) > 0 {
		mGenre = fmt.Sprintf(`EXISTS (SELECT 1 FROM book_genres bg2 JOIN genres g2 ON g2.id = bg2.genre_id
                 WHERE bg2.book_id = s.id AND g2.name = ANY(%s))`, arg(sel.Genres))
	}
	if len(sel.Tags) > 0 {
		mTag = fmt.Sprintf(`EXISTS (SELECT 1 FROM book_tags bt2 JOIN tags t2 ON t2.id = bt2.tag_id
                 WHERE bt2.book_id = s.id AND bt2.deleted_at IS NULL AND t2.name = ANY(%s))`, arg(sel.Tags))
	}
	// By id, not name: a shelf name is only unique within its library, so two
	// libraries can both have "Favourites" and they are different shelves.
	if len(sel.Shelves) > 0 {
		mShelf = fmt.Sprintf(`EXISTS (SELECT 1 FROM library_shelf_books bsh2
                 WHERE bsh2.book_id = s.id AND bsh2.shelf_id = ANY(%s))`, arg(sel.Shelves))
	}
	if len(sel.Ratings) > 0 {
		mRating = fmt.Sprintf(`x.rating = ANY(%s)`, arg(sel.Ratings))
	}
	if len(sel.Favourites) > 0 {
		// COALESCE because a book the caller has never touched has no
		// interaction row, and "no row" means not a favourite rather than
		// unknown. Without it every such book drops out of both sides.
		mFav = fmt.Sprintf(`COALESCE(x.is_favorite, false) = ANY(%s)`, arg(sel.Favourites))
	}

	// Every flag except the dimension's own.
	others := func(skip string) string {
		all := map[string]string{
			"own": "f.m_own",
			"lib": "f.m_lib", "status": "f.m_status", "type": "f.m_type",
			"genre": "f.m_genre", "tag": "f.m_tag", "shelf": "f.m_shelf",
			"rating": "f.m_rating", "fav": "f.m_fav",
		}
		parts := make([]string, 0, 8)
		for k, v := range all {
			if k != skip {
				parts = append(parts, v)
			}
		}
		insertionSort(parts) // deterministic SQL, so it diffs cleanly in logs
		return strings.Join(parts, " AND ")
	}

	q := fmt.Sprintf(`
WITH scope AS (
    SELECT DISTINCT b.id, b.media_type_id, lb.ownership
    FROM books b
    JOIN (%s) lb ON lb.book_id = b.id
    WHERE TRUE %s
),
inter AS (%s),
f AS (
    SELECT s.id, s.media_type_id, s.ownership,
           COALESCE(x.read_status, 'unread') AS read_status,
           x.rating, COALESCE(x.is_favorite, false) AS is_favorite,
           %s AS m_own, %s AS m_lib, %s AS m_status, %s AS m_type,
           %s AS m_genre, %s AS m_tag, %s AS m_shelf, %s AS m_rating,
           %s AS m_fav
    FROM scope s LEFT JOIN inter x ON x.book_id = s.id
)
SELECT 'ownership' AS dim, f.ownership AS value, f.ownership AS label, COUNT(*) AS n
FROM f WHERE %s GROUP BY f.ownership
UNION ALL
SELECT 'library', l.id::text, l.name, COUNT(DISTINCT f.id)
FROM f JOIN held_books lb3 ON lb3.book_id = f.id AND lb3.deleted_at IS NULL
       JOIN libraries l ON l.id = lb3.library_id
WHERE lb3.library_id = ANY($1) AND %s
GROUP BY l.id, l.name
UNION ALL
SELECT 'read_status', f.read_status, f.read_status, COUNT(*)
FROM f WHERE %s GROUP BY f.read_status
UNION ALL
SELECT 'media_type', mt.name, mt.display_name, COUNT(*)
FROM f JOIN media_types mt ON mt.id = f.media_type_id
WHERE %s GROUP BY mt.name, mt.display_name
UNION ALL
SELECT 'genre', g.name, g.name, COUNT(*)
FROM f JOIN book_genres bg ON bg.book_id = f.id JOIN genres g ON g.id = bg.genre_id
WHERE %s GROUP BY g.name
UNION ALL
SELECT 'tag', t.name, t.name, COUNT(*)
FROM f JOIN book_tags bt ON bt.book_id = f.id AND bt.deleted_at IS NULL
       JOIN tags t ON t.id = bt.tag_id AND t.deleted_at IS NULL
WHERE %s GROUP BY t.name
UNION ALL
SELECT 'shelf', sh.id::text, sh.name, COUNT(DISTINCT f.id)
FROM f JOIN library_shelf_books bsh ON bsh.book_id = f.id
       JOIN library_shelves sh ON sh.id = bsh.shelf_id
WHERE sh.library_id = ANY($1) AND %s
GROUP BY sh.id, sh.name
UNION ALL
SELECT 'rating', f.rating::text, f.rating::text, COUNT(*)
FROM f WHERE f.rating IS NOT NULL AND %s GROUP BY f.rating
UNION ALL
SELECT 'favourite', f.is_favorite::text, f.is_favorite::text, COUNT(*)
FROM f WHERE %s GROUP BY f.is_favorite
ORDER BY 1, 4 DESC, 3`,
		// The scope CTE comes first now: it holds the caller's own placeholder,
		// so it has to be built before the text filter's is counted.
		scopeSQL, textWhere, interCTE,
		mOwn, mLib, mStatus, mType, mGenre, mTag, mShelf, mRating, mFav,
		others("own"),
		others("lib"), others("status"), others("type"),
		others("genre"), others("tag"), others("shelf"), others("rating"),
		others("fav"))

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("counting book facets: %w", err)
	}
	defer rows.Close()

	out := emptyFacets()
	for rows.Next() {
		var dim string
		var fv FacetValue
		if err := rows.Scan(&dim, &fv.Value, &fv.Label, &fv.Count); err != nil {
			return nil, fmt.Errorf("scanning facet row: %w", err)
		}
		switch dim {
		case "ownership":
			out.Ownership = append(out.Ownership, fv)
		case "library":
			out.Library = append(out.Library, fv)
		case "read_status":
			out.ReadStatus = append(out.ReadStatus, fv)
		case "media_type":
			out.MediaType = append(out.MediaType, fv)
		case "genre":
			out.Genre = append(out.Genre, fv)
		case "tag":
			out.Tag = append(out.Tag, fv)
		case "shelf":
			out.Shelf = append(out.Shelf, fv)
		case "rating":
			out.Rating = append(out.Rating, fv)
		case "favourite":
			out.Favourite = append(out.Favourite, fv)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Closed vocabularies come back complete, zero-filled and in their own
	// order.
	//
	// A GROUP BY only emits values something matched, which is right for
	// genres and tags — nobody wants every unused tag listed at nought. It is
	// wrong for a fixed set, because the filter then disappears from the rail
	// exactly when the reader wants to know the answer is none. A collection
	// with nothing on a wishlist offered no Wishlist filter at all, so "how
	// many am I missing" had no way to be asked, and the same omission left
	// every saved view built on a status with no number beside it.
	out.Ownership = fillClosed(out.Ownership, OwnershipValues)
	out.ReadStatus = fillClosed(out.ReadStatus, ReadStatusValues)
	out.Favourite = fillClosed(out.Favourite, FavouriteValues)

	return out, nil
}

// ReadStatusValues is the read-status vocabulary, in the order a reader moves
// through it. Mirrors the priority reading state used to be collapsed by, which
// status wins for a book with several editions rather than about display.
var ReadStatusValues = []string{"unread", "reading", "read", "did_not_finish"}

// FavouriteValues is the starred flag as a facet. Both sides are emitted so a
// Favourites view still shows a nought when nothing is starred yet; clients
// generally render only the true row.
var FavouriteValues = []string{"true", "false"}

// fillClosed returns the facet in vocabulary order with every missing value
// present at zero, and anything unexpected kept on the end rather than dropped:
// a value the database holds but this list has not heard of is a schema change,
// and silently hiding it would make that change invisible.
func fillClosed(got []FacetValue, vocab []string) []FacetValue {
	byValue := make(map[string]FacetValue, len(got))
	for _, fv := range got {
		byValue[fv.Value] = fv
	}
	out := make([]FacetValue, 0, len(vocab)+len(got))
	for _, v := range vocab {
		if fv, ok := byValue[v]; ok {
			out = append(out, fv)
			delete(byValue, v)
			continue
		}
		out = append(out, FacetValue{Value: v, Label: v, Count: 0})
	}
	for _, fv := range got {
		if _, unseen := byValue[fv.Value]; unseen {
			out = append(out, fv)
			delete(byValue, fv.Value)
		}
	}
	return out
}

// insertionSort keeps a six-element slice ordered without reaching for sort.
func insertionSort(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ListAcross lists books spanning several libraries, deduplicated: a work held
// by two libraries appears once, not twice.
func (r *BookRepo) ListAcross(ctx context.Context, libraryIDs []uuid.UUID, opts ListBooksOpts) ([]*models.Book, int, error) {
	if len(libraryIDs) == 0 {
		return []*models.Book{}, 0, nil
	}
	return r.listScoped(ctx, libraryIDs, opts)
}
