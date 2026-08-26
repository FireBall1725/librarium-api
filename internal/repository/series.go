// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SeriesRepo struct {
	db *pgxpool.Pool
}

func NewSeriesRepo(db *pgxpool.Pool) *SeriesRepo {
	return &SeriesRepo{db: db}
}

const seriesTagsSubquery = `
    COALESCE(
        (SELECT json_agg(json_build_object('id', t.id, 'name', t.name, 'color', t.color) ORDER BY t.name)
         FROM series_tags st JOIN tags t ON t.id = st.tag_id WHERE st.series_id = s.id),
        '[]'::json
    )`

// readStateSubqueries returns the SELECT-list expressions for `read_count`
// and `reading_count` against the caller's effective read status. The
// effective status uses the same priority ordering as booksSelect — read >
// reading > did_not_finish — so series indicators match book-cover badges.
// When callerArg is 0, both counts are emitted as 0 so the column count
// stays stable.
func readStateSubqueries(callerArg int) string {
	if callerArg == 0 {
		return `0 AS read_count, 0 AS reading_count`
	}
	return seriesReadCountExpr(callerArg) + " AS read_count, " +
		seriesReadingCountExpr(callerArg) + " AS reading_count"
}

// How many books of a run the caller has finished, and how many they are part
// way through.
//
// Split out of readStateSubqueries because filtering and sorting on read state
// needs the bare expression: HAVING and ORDER BY run before the SELECT list's
// aliases exist, so the expression has to be repeated rather than named.
func seriesReadCountExpr(callerArg int) string {
	return seriesStatusCountExpr(callerArg, "read")
}

func seriesReadingCountExpr(callerArg int) string {
	return seriesStatusCountExpr(callerArg, "reading")
}

func seriesStatusCountExpr(callerArg int, status string) string {
	return fmt.Sprintf(`
       (SELECT COUNT(*)::int FROM book_series bs2
          JOIN books b ON b.id = bs2.book_id
          WHERE bs2.series_id = s.id AND (
              SELECT ubi.read_status FROM user_books ubi
              WHERE ubi.book_id = b.id AND ubi.user_id = $%d AND ubi.deleted_at IS NULL
          ) = '%s')`, callerArg, status)
}

// seriesBookCountExpr counts the volumes of a series the library actually holds.
//
// It used to be a plain COUNT over book_series, which counts every book recorded
// against the series whatever its ownership: wishlist entries, AI suggestions,
// and the gap rows that exist precisely to represent volumes nobody has. Those
// are all real book_series rows, so a series you own half of counted as whole.
// Every series on the index reported "own N of N" and wore a "complete" badge
// while the Books page, which filters on ownership, disagreed about the same
// series in the same session.
//
// Holding is a row in held_books, matching the shelf arm of bookScopeCTE. The
// series' own library is the one asked about: a series record belongs to one
// library and its counts describe that library's copies, which is why a series
// held twice is two rows rather than a merged total.
//
// Positions, not books, because the label says volumes. A complete 56-volume
// run plus one three-in-one read "57 / 56", and so did the standard edition of
// Bleach volume one beside its 20th-anniversary reprint: two books, one volume
// of the run either way. A container covers what it holds, so owning only the
// three-in-one counts as three, which is what the ownership rule already says
// on the Books page.
// seriesGenresSubquery reads a series' genres from the shared vocabulary.
//
// It replaced the free-text column of the same name, which was never checked
// against anything: "Science Fiction", "Science fiction" and "Sci-Fi" were
// three separate values on one facet, and "Comics" sat among them describing a
// format rather than a genre. Books have always keyed genre through a join into
// this table; series do now, so the word means the same thing on both surfaces.
//
// Ordered by name so the array is stable across reads and two identical series
// cannot render differently.
const seriesGenresSubquery = `(
	SELECT COALESCE(array_agg(g2.name ORDER BY g2.name), '{}')
	  FROM series_genres sg JOIN genres g2 ON g2.id = sg.genre_id
	 WHERE sg.series_id = s.id
)`

// seriesBookCountValue is the same count without its alias, for the places that
// run before the SELECT list exists.
const seriesBookCountValue = `(
	           SELECT count(DISTINCT covered.position)
	             FROM book_series bs2
	             JOIN held_books lb
	               ON lb.book_id = bs2.book_id
	              AND lb.library_id = s.library_id
	              AND lb.deleted_at IS NULL
	             CROSS JOIN LATERAL (
	                     SELECT bs2.position
	                 UNION
	                     SELECT within.position
	                       FROM book_contents bc
	                       JOIN book_series within
	                         ON within.book_id = bc.contained_id
	                        AND within.series_id = bs2.series_id
	                      WHERE bc.container_id = bs2.book_id
	             ) covered
	            WHERE bs2.series_id = s.id
	       )`

const seriesBookCountExpr = seriesBookCountValue + ` AS book_count`

// ─── Series CRUD ──────────────────────────────────────────────────────────────

// List is the per-library entry point.
func (r *SeriesRepo) List(ctx context.Context, libraryID, callerID uuid.UUID, search, tagFilter string) ([]*models.Series, error) {
	return r.listScoped(ctx, []uuid.UUID{libraryID}, callerID, search, tagFilter, defaultPreviewBooks, SeriesFilter{})
}

// ListAcross lists series spanning several libraries, for the cross-library
// Series surface. Series are a per-library record, so a series held by two
// libraries is genuinely two rows and appears twice rather than being merged:
// the counts on each row describe that library's copies, and collapsing them
// would report a total nobody owns.
//
// previewBooks caps the volumes returned per series. The index draws a strip of
// every volume it can, so it asks for far more than the four a detail panel
// needs, but still a bounded number: a series with a thousand entries must not
// become a thousand-element payload per row.
func (r *SeriesRepo) ListAcross(ctx context.Context, libraryIDs []uuid.UUID, callerID uuid.UUID, search string, previewBooks int) ([]*models.Series, error) {
	if len(libraryIDs) == 0 {
		return []*models.Series{}, nil
	}
	return r.listScoped(ctx, libraryIDs, callerID, search, "", previewBooks, SeriesFilter{})
}

// SeriesFilter narrows and orders the cross-library index.
//
// Every one of these used to be a client-side pass over whatever the server
// happened to send, which worked only because the per-library page never had
// more than one library's worth of rows to filter. Across every library the
// list is the whole instance, so the narrowing has to happen where the rows
// are.
type SeriesFilter struct {
	// Libraries narrows within the caller's readable set. A dimension of the
	// rail rather than a folder, and multi-select like the one on Books:
	// ticking two answers "either of these", which is what a checkbox list
	// means everywhere else in the product.
	Libraries []uuid.UUID
	// MediaTypes narrows to runs holding a book of that kind. Read off the
	// books rather than stored on the series: nothing says a series is manga,
	// every book in it does.
	MediaTypes []string
	// Genres narrows on the series' own free-text list, which is a different
	// vocabulary from the controlled one book genres use. Overlap, not
	// containment: ticking two means either.
	Genres []string
	// Ratings narrows on the run's average, as whole stored points. Ticking
	// several means any of them, the same as every other dimension.
	Ratings []int32
	// MyRatings is the same against the caller's own average.
	MyRatings []int32
	// Status is a publication status: ongoing, completed, hiatus, cancelled.
	Status string
	// Arcs is "with" or "without". Empty means either.
	Arcs string
	// Reading is unread, reading, or read_all, derived from the caller's own
	// progress through the run rather than stored anywhere.
	Reading string
	Tag     string
	// Sort is name, volumes, missing, read, or recent. Empty means name.
	Sort string
	Desc bool
}

// ListAcrossFiltered is the index behind /me/series.
func (r *SeriesRepo) ListAcrossFiltered(
	ctx context.Context, libraryIDs []uuid.UUID, callerID uuid.UUID,
	search string, previewBooks int, f SeriesFilter,
) ([]*models.Series, error) {
	if len(libraryIDs) == 0 {
		return []*models.Series{}, nil
	}
	return r.listScoped(ctx, libraryIDs, callerID, search, f.Tag, previewBooks, f)
}

// defaultPreviewBooks is what a series card shows: enough to suggest the run
// without paying for the whole thing.
const defaultPreviewBooks = 4

func (r *SeriesRepo) listScoped(ctx context.Context, libraryIDs []uuid.UUID, callerID uuid.UUID, search, tagFilter string, previewBooks int, f SeriesFilter) ([]*models.Series, error) {
	if previewBooks <= 0 {
		previewBooks = defaultPreviewBooks
	}
	args := []any{libraryIDs}
	where := `WHERE s.library_id = ANY($1)`
	if search != "" {
		args = append(args, "%"+search+"%")
		where += fmt.Sprintf(` AND lower(s.name) LIKE lower($%d)`, len(args))
	}
	if tagFilter != "" {
		args = append(args, tagFilter)
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM series_tags st JOIN tags t ON t.id = st.tag_id WHERE st.series_id = s.id AND lower(t.name) = lower($%d))`, len(args))
	}
	if len(f.Libraries) > 0 {
		args = append(args, f.Libraries)
		where += fmt.Sprintf(` AND s.library_id = ANY($%d)`, len(args))
	}
	if len(f.MediaTypes) > 0 {
		args = append(args, f.MediaTypes)
		where += " AND " + seriesMediaTypeExists("s.id", fmt.Sprintf("$%d", len(args)))
	}
	if len(f.Ratings) > 0 {
		args = append(args, f.Ratings)
		where += fmt.Sprintf(` AND %s = ANY($%d)`, seriesAvgRatingScalar("$1"), len(args))
	}
	if len(f.MyRatings) > 0 && callerID != uuid.Nil {
		// Bound here rather than reusing callerArg, which is claimed further
		// down: the numbering follows the order arguments are appended in.
		args = append(args, callerID)
		me := fmt.Sprintf("$%d", len(args))
		args = append(args, f.MyRatings)
		where += fmt.Sprintf(` AND %s = ANY($%d)`, seriesMyRatingScalar(me), len(args))
	}
	if len(f.Genres) > 0 {
		args = append(args, f.Genres)
		where += fmt.Sprintf(` AND EXISTS (
			SELECT 1 FROM series_genres sg_f JOIN genres g_f ON g_f.id = sg_f.genre_id
			 WHERE sg_f.series_id = s.id AND g_f.name = ANY($%d))`, len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(` AND s.status = $%d`, len(args))
	}
	callerArg := 0
	if callerID != uuid.Nil {
		args = append(args, callerID)
		callerArg = len(args)
	}

	// Arcs, reading state and the sort all key off values the SELECT computes,
	// so they cannot sit in the WHERE beside the library scope. HAVING runs
	// after the aggregate, which is exactly where they belong; repeating the
	// expression rather than naming the alias is what Postgres requires there.
	having := ""
	addHaving := func(cond string) {
		if having == "" {
			having = " HAVING " + cond
		} else {
			having += " AND " + cond
		}
	}
	const arcCount = `(SELECT COUNT(*) FROM series_arcs sa WHERE sa.series_id = s.id)`
	switch f.Arcs {
	case "with":
		addHaving(arcCount + " > 0")
	case "without":
		addHaving(arcCount + " = 0")
	}
	// Read state describes how far the caller is through the run, so an
	// anonymous read has no opinion to filter on and every arm would match
	// nothing. Left alone rather than returning an empty list.
	if callerArg > 0 {
		switch f.Reading {
		case "unread":
			addHaving(seriesReadCountExpr(callerArg) + " = 0 AND " + seriesReadingCountExpr(callerArg) + " = 0")
		case "reading":
			addHaving(seriesReadingCountExpr(callerArg) + " > 0")
		case "read_all":
			addHaving(seriesBookCountValue + " > 0 AND " +
				seriesReadCountExpr(callerArg) + " >= " + seriesBookCountValue)
		}
	}

	// The run's own rating, and the caller's. Scoped to the libraries in play,
	// matching the book average exactly: an instance can host two households
	// that share no library, and one household's opinions have no business
	// moving the other's number.
	myRating := "NULL::int"
	if callerArg > 0 {
		myRating = seriesMyRatingScalar(fmt.Sprintf("$%d", callerArg))
	}
	ratingExpr := seriesAvgRatingScalar("$1") + " AS rating, " +
		seriesRatedBooksScalar("$1") + " AS rated_books, " +
		myRating + " AS my_rating"

	dir := "ASC"
	if f.Desc {
		dir = "DESC"
	}
	// NULLS LAST on every numeric sort, so a series with no total or no
	// progress falls to the end rather than heading a list it says nothing
	// about. Name is the tiebreak everywhere, which keeps the order stable
	// across reloads when the sort key ties.
	order := " ORDER BY lower(s.name) " + dir
	switch f.Sort {
	case "volumes":
		order = " ORDER BY " + seriesBookCountValue + " " + dir + " NULLS LAST, lower(s.name) ASC"
	case "missing":
		order = " ORDER BY GREATEST(COALESCE(s.total_count, 0) - " + seriesBookCountValue + ", 0) " +
			dir + " NULLS LAST, lower(s.name) ASC"
	case "read":
		if callerArg > 0 {
			order = " ORDER BY " + seriesReadCountExpr(callerArg) + " " + dir + " NULLS LAST, lower(s.name) ASC"
		}
	case "rating":
		// NULLS LAST both ways. A run nobody has rated is not the worst run;
		// it is a run with nothing to say, and it belongs at the end whichever
		// direction the reader asked for.
		order = " ORDER BY " + seriesAvgRatingScalar("$1") + " " + dir +
			" NULLS LAST, lower(s.name) ASC"
	case "recent":
		order = " ORDER BY s.updated_at " + dir + ", lower(s.name) ASC"
	}

	q := `
		SELECT s.id, s.library_id, s.name, COALESCE(s.description,''),
		       s.total_count, s.status, s.original_language, s.publication_year,
		       s.demographic, ` + seriesGenresSubquery + `, COALESCE(s.url,''),
		       COALESCE(s.external_id,''), COALESCE(s.external_source,''),
		       (SELECT MAX(sv.release_date) FROM series_volumes sv WHERE sv.series_id = s.id AND sv.release_date <= CURRENT_DATE) AS last_release_date,
		       (SELECT MIN(sv.release_date) FROM series_volumes sv WHERE sv.series_id = s.id AND sv.release_date > CURRENT_DATE) AS next_release_date,
		       ` + seriesBookCountExpr + `,
		       (SELECT COUNT(*) FROM series_arcs sa WHERE sa.series_id = s.id) AS arc_count,
		       ` + readStateSubqueries(callerArg) + `,
		       ` + ratingExpr + `,
		       (
		           SELECT COALESCE(json_agg(jsonb_build_object(
		               'book_id', t.id,
		               'title', t.title,
		               'updated_at', t.updated_at,
		               'has_cover', t.has_cover,
		               'held', t.held
		           ) ORDER BY t.position), '[]'::json)
		           FROM (
		               SELECT b.id, b.title, b.updated_at, bs2.position,
		                      EXISTS(SELECT 1 FROM cover_images ci
		                             WHERE ci.entity_type='book' AND ci.entity_id=b.id AND ci.is_primary=true) AS has_cover,
		                      -- Whether this library actually has it. The strip
		                      -- shows every volume of the run, promoted gaps
		                      -- included, so without this a volume nobody owns
		                      -- is drawn exactly like one on the shelf.
		                      EXISTS(SELECT 1 FROM held_books hb
		                             WHERE hb.book_id = b.id
		                               AND hb.library_id = s.library_id
		                               AND hb.deleted_at IS NULL) AS held
		               FROM book_series bs2 JOIN books b ON b.id = bs2.book_id
		               WHERE bs2.series_id = s.id
		               ORDER BY bs2.position
		               LIMIT ` + strconv.Itoa(previewBooks) + `
		           ) t
		       ) AS preview_books,
		       s.created_at, s.updated_at,
		       ` + seriesTagsSubquery + ` AS tags
		FROM series s
		LEFT JOIN book_series bs ON bs.series_id = s.id
		` + where + `
		GROUP BY s.id` + having + order

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing series: %w", err)
	}
	defer rows.Close()

	var out []*models.Series
	for rows.Next() {
		s, err := scanSeries(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *SeriesRepo) FindByID(ctx context.Context, id, callerID uuid.UUID) (*models.Series, error) {
	args := []any{id}
	callerArg := 0
	if callerID != uuid.Nil {
		args = append(args, callerID)
		callerArg = len(args)
	}
	// One series, so the scope is the library it belongs to. There is no
	// readable set to intersect with here: the caller already had to be allowed
	// through to reach this row at all.
	myRating := "NULL::int"
	if callerArg > 0 {
		myRating = seriesMyRatingScalar(fmt.Sprintf("$%d", callerArg))
	}
	ratingExpr := seriesAvgRatingScalar("ARRAY[s.library_id]") + " AS rating, " +
		seriesRatedBooksScalar("ARRAY[s.library_id]") + " AS rated_books, " +
		myRating + " AS my_rating"

	q := `
		SELECT s.id, s.library_id, s.name, COALESCE(s.description,''),
		       s.total_count, s.status, s.original_language, s.publication_year,
		       s.demographic, ` + seriesGenresSubquery + `, COALESCE(s.url,''),
		       COALESCE(s.external_id,''), COALESCE(s.external_source,''),
		       (SELECT MAX(sv.release_date) FROM series_volumes sv WHERE sv.series_id = s.id AND sv.release_date <= CURRENT_DATE) AS last_release_date,
		       (SELECT MIN(sv.release_date) FROM series_volumes sv WHERE sv.series_id = s.id AND sv.release_date > CURRENT_DATE) AS next_release_date,
		       ` + seriesBookCountExpr + `,
		       (SELECT COUNT(*) FROM series_arcs sa WHERE sa.series_id = s.id) AS arc_count,
		       ` + readStateSubqueries(callerArg) + `,
		       ` + ratingExpr + `,
		       (
		           SELECT COALESCE(json_agg(jsonb_build_object(
		               'book_id', t.id,
		               'title', t.title,
		               'updated_at', t.updated_at,
		               'has_cover', t.has_cover,
		               'held', t.held
		           ) ORDER BY t.position), '[]'::json)
		           FROM (
		               SELECT b.id, b.title, b.updated_at, bs2.position,
		                      EXISTS(SELECT 1 FROM cover_images ci
		                             WHERE ci.entity_type='book' AND ci.entity_id=b.id AND ci.is_primary=true) AS has_cover,
		                      -- Whether this library actually has it. The strip
		                      -- shows every volume of the run, promoted gaps
		                      -- included, so without this a volume nobody owns
		                      -- is drawn exactly like one on the shelf.
		                      EXISTS(SELECT 1 FROM held_books hb
		                             WHERE hb.book_id = b.id
		                               AND hb.library_id = s.library_id
		                               AND hb.deleted_at IS NULL) AS held
		               FROM book_series bs2 JOIN books b ON b.id = bs2.book_id
		               WHERE bs2.series_id = s.id
		               ORDER BY bs2.position
		               LIMIT 4
		           ) t
		       ) AS preview_books,
		       s.created_at, s.updated_at,
		       ` + seriesTagsSubquery + ` AS tags
		FROM series s
		LEFT JOIN book_series bs ON bs.series_id = s.id
		WHERE s.id = $1
		GROUP BY s.id`
	s, err := scanSeries(r.db.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding series: %w", err)
	}
	return s, nil
}

func (r *SeriesRepo) Create(ctx context.Context, id, libraryID uuid.UUID, name, description string, totalCount *int, status, originalLanguage string, publicationYear *int, demographic string, genres []string, url string, externalID, externalSource string, createdBy uuid.UUID) (*models.Series, error) {
	if status == "" {
		status = "ongoing"
	}
	if genres == nil {
		genres = []string{}
	}
	const q = `
		INSERT INTO series (id, library_id, name, description, total_count, status, original_language, publication_year, demographic, genres, url, external_id, external_source, created_by)
		VALUES ($1, $2, $3, NULLIF($4,''), $5, $6, NULLIF($7,''), $8, NULLIF($9,''), $10, NULLIF($11,''), NULLIF($12,''), NULLIF($13,''), $14)`
	if _, err := r.db.Exec(ctx, q, id, libraryID, name, description, totalCount, status, originalLanguage, publicationYear, demographic, genres, url, externalID, externalSource, createdBy); err != nil {
		return nil, fmt.Errorf("inserting series: %w", err)
	}
	if err := r.setGenres(ctx, id, genres); err != nil {
		return nil, err
	}
	return r.FindByID(ctx, id, uuid.Nil)
}

func (r *SeriesRepo) Update(ctx context.Context, id uuid.UUID, name, description string, totalCount *int, status, originalLanguage string, publicationYear *int, demographic string, genres []string, url string, externalID, externalSource string) (*models.Series, error) {
	if status == "" {
		status = "ongoing"
	}
	if genres == nil {
		genres = []string{}
	}
	const q = `
		UPDATE series
		SET name             = $2,
		    description      = NULLIF($3,''),
		    total_count      = $4,
		    status           = $5,
		    original_language = NULLIF($6,''),
		    publication_year = $7,
		    demographic      = NULLIF($8,''),
		    genres           = $9,
		    url              = NULLIF($10,''),
		    external_id      = NULLIF($11,''),
		    external_source  = NULLIF($12,''),
		    updated_at       = NOW()
		WHERE id = $1`
	result, err := r.db.Exec(ctx, q, id, name, description, totalCount, status, originalLanguage, publicationYear, demographic, genres, url, externalID, externalSource)
	if err != nil {
		return nil, fmt.Errorf("updating series: %w", err)
	}
	if result.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if err := r.setGenres(ctx, id, genres); err != nil {
		return nil, err
	}
	return r.FindByID(ctx, id, uuid.Nil)
}

// setGenres keys a series into the shared genre vocabulary.
//
// Names in, ids resolved here, because a caller editing a series is picking
// words out of a list and should not have to know the list is a table.
//
// A name that is not in the vocabulary is dropped rather than created. Letting
// anything invent a genre is exactly what produced the free-text column this
// replaced, where one facet counted "Sci-Fi" beside "Science Fiction" beside
// "Science fiction" and none of them knew about the others.
func (r *SeriesRepo) setGenres(ctx context.Context, seriesID uuid.UUID, names []string) error {
	if _, err := r.db.Exec(ctx,
		`DELETE FROM series_genres WHERE series_id = $1`, seriesID); err != nil {
		return fmt.Errorf("clearing series genres: %w", err)
	}
	if len(names) == 0 {
		return nil
	}
	if _, err := r.db.Exec(ctx, `
		INSERT INTO series_genres (series_id, genre_id)
		SELECT $1, g.id FROM genres g
		 WHERE g.name_lower = ANY(SELECT lower(trim(n)) FROM unnest($2::text[]) AS n)
		ON CONFLICT DO NOTHING`, seriesID, names); err != nil {
		return fmt.Errorf("setting series genres: %w", err)
	}
	return nil
}

func (r *SeriesRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `DELETE FROM series WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting series: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Series entries ───────────────────────────────────────────────────────────

func (r *SeriesRepo) ListBooks(ctx context.Context, seriesID, callerID uuid.UUID) ([]*models.SeriesEntry, error) {
	args := []any{seriesID}
	userStatusExpr := `'' AS user_read_status`
	if callerID != uuid.Nil {
		args = append(args, callerID)
		userStatusExpr = fmt.Sprintf(`COALESCE((
			SELECT ubi.read_status
			FROM user_books ubi
			WHERE ubi.book_id = b.id AND ubi.user_id = $%d AND ubi.deleted_at IS NULL
		), '') AS user_read_status`, len(args))
	}
	q := `
		SELECT
			bs.position,
			-- The span a container covers. A three-in-one sits at volume one
			-- and reads as a second volume one beside the single; this is what
			-- lets it say "1 to 3" instead. Restricted to this series so an
			-- anthology drawing from several runs does not claim a span in a
			-- run it only partly holds.
			COALESCE((
				SELECT max(inner_bs.position)
				  FROM book_contents bc
				  JOIN book_series inner_bs
				    ON inner_bs.book_id = bc.contained_id
				   AND inner_bs.series_id = bs.series_id
				 WHERE bc.container_id = b.id
			), 0) AS position_end,
			b.id, b.title, COALESCE(b.subtitle,''), mt.display_name,
			bs.arc_id,
			b.updated_at,
			EXISTS(SELECT 1 FROM cover_images ci
			       WHERE ci.entity_type='book' AND ci.entity_id=b.id AND ci.is_primary=true) AS has_cover,
			-- Whether this library actually has it. Every volume of the run is
			-- listed, promoted gaps included, so a client needs to be able to
			-- tell one nobody owns from one on the shelf.
			EXISTS(SELECT 1 FROM held_books hb
			       WHERE hb.book_id = b.id AND hb.library_id = se_h.library_id
			         AND hb.deleted_at IS NULL) AS held,
			` + userStatusExpr + `,
			(
				SELECT COALESCE(
					json_agg(
						json_build_object(
							'contributor_id', c.id,
							'name', c.name,
							'role', bc.role,
							'display_order', bc.display_order
						) ORDER BY bc.display_order, c.name
					),
					'[]'::json
				)
				FROM book_contributors bc
				JOIN contributors c ON c.id = bc.contributor_id
				WHERE bc.book_id = b.id
			) AS contributors
		FROM book_series bs
		JOIN books b ON b.id = bs.book_id
		JOIN media_types mt ON mt.id = b.media_type_id
		JOIN series se_h ON se_h.id = bs.series_id
		WHERE bs.series_id = $1
		ORDER BY bs.position`
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing series books: %w", err)
	}
	defer rows.Close()

	var out []*models.SeriesEntry
	for rows.Next() {
		entry, err := scanSeriesEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

// MatchCandidate is a library book that is NOT in the target series.
// OtherSeries lists any series it is already in, so the UI can warn before
// double-assigning.
type MatchCandidate struct {
	BookID      uuid.UUID
	Title       string
	Subtitle    string
	OtherSeries []models.BookSeriesRef
}

// ListMatchCandidates returns every book in the given library that is not
// already a member of the target series, along with any other series each
// book already belongs to. Titles are returned in alphabetical order.
func (r *SeriesRepo) ListMatchCandidates(ctx context.Context, libraryID, seriesID uuid.UUID) ([]*MatchCandidate, error) {
	const q = `
		SELECT
			b.id, b.title, COALESCE(b.subtitle,''),
			COALESCE(
				(SELECT json_agg(json_build_object('series_id', s2.id, 'series_name', s2.name, 'position', bs2.position) ORDER BY s2.name)
				 FROM book_series bs2 JOIN series s2 ON s2.id = bs2.series_id
				 WHERE bs2.book_id = b.id AND bs2.series_id <> $2),
				'[]'::json
			) AS other_series
		FROM books b
		JOIN held_books lb ON lb.book_id = b.id
		WHERE lb.library_id = $1
		  AND NOT EXISTS (
		      SELECT 1 FROM book_series bs
		      WHERE bs.book_id = b.id AND bs.series_id = $2
		  )
		ORDER BY b.title`
	rows, err := r.db.Query(ctx, q, libraryID, seriesID)
	if err != nil {
		return nil, fmt.Errorf("listing match candidates: %w", err)
	}
	defer rows.Close()

	var out []*MatchCandidate
	for rows.Next() {
		var (
			c           MatchCandidate
			pgBookID    pgtype.UUID
			otherJSON   []byte
			otherParsed []struct {
				SeriesID   string  `json:"series_id"`
				SeriesName string  `json:"series_name"`
				Position   float64 `json:"position"`
			}
		)
		if err := rows.Scan(&pgBookID, &c.Title, &c.Subtitle, &otherJSON); err != nil {
			return nil, err
		}
		c.BookID = uuid.UUID(pgBookID.Bytes)
		if err := json.Unmarshal(otherJSON, &otherParsed); err == nil {
			for _, o := range otherParsed {
				sid, err := uuid.Parse(o.SeriesID)
				if err != nil {
					continue
				}
				c.OtherSeries = append(c.OtherSeries, models.BookSeriesRef{
					SeriesID:   sid,
					SeriesName: o.SeriesName,
					Position:   o.Position,
				})
			}
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// OrphanBook is a library book not yet assigned to any series. Used by the
// series-suggestion flow to scan titles and propose groupings.
type OrphanBook struct {
	BookID    uuid.UUID
	Title     string
	Subtitle  string
	HasCover  bool
	CreatedAt pgtype.Timestamptz
}

// ListOrphanBooks returns every book in the library not in any series,
// filtered by the given media-type names. When mediaTypes is empty, all
// media types are eligible.
func (r *SeriesRepo) ListOrphanBooks(ctx context.Context, libraryID uuid.UUID, mediaTypes []string) ([]*OrphanBook, error) {
	const q = `
		SELECT
			b.id, b.title, COALESCE(b.subtitle,''), b.created_at,
			EXISTS(
				SELECT 1 FROM cover_images ci
				WHERE ci.entity_type = 'book' AND ci.entity_id = b.id AND ci.is_primary = true
			) AS has_cover
		FROM books b
		JOIN media_types mt ON mt.id = b.media_type_id
		JOIN held_books lb ON lb.book_id = b.id
		WHERE lb.library_id = $1
		  AND (cardinality($2::text[]) = 0 OR mt.name = ANY($2))
		  AND NOT EXISTS (SELECT 1 FROM book_series bs WHERE bs.book_id = b.id)
		ORDER BY b.title`
	rows, err := r.db.Query(ctx, q, libraryID, mediaTypes)
	if err != nil {
		return nil, fmt.Errorf("listing orphan books: %w", err)
	}
	defer rows.Close()

	var out []*OrphanBook
	for rows.Next() {
		var (
			b        OrphanBook
			pgBookID pgtype.UUID
		)
		if err := rows.Scan(&pgBookID, &b.Title, &b.Subtitle, &b.CreatedAt, &b.HasCover); err != nil {
			return nil, err
		}
		b.BookID = uuid.UUID(pgBookID.Bytes)
		out = append(out, &b)
	}
	return out, rows.Err()
}

func (r *SeriesRepo) UpsertBook(ctx context.Context, seriesID, bookID uuid.UUID, position float64) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO book_series (book_id, series_id, position)
		VALUES ($1, $2, $3)
		ON CONFLICT (book_id, series_id) DO UPDATE SET position = EXCLUDED.position`,
		bookID, seriesID, position,
	)
	if err != nil {
		return fmt.Errorf("upserting series book: %w", err)
	}
	return nil
}

func (r *SeriesRepo) GetSeriesForBook(ctx context.Context, libraryID, bookID uuid.UUID) ([]*models.BookSeriesRef, error) {
	const q = `
		SELECT s.id, s.name, bs.position
		FROM series s
		JOIN book_series bs ON bs.series_id = s.id
		WHERE s.library_id = $1 AND bs.book_id = $2
		ORDER BY bs.position`
	rows, err := r.db.Query(ctx, q, libraryID, bookID)
	if err != nil {
		return nil, fmt.Errorf("getting series for book: %w", err)
	}
	defer rows.Close()

	out := []*models.BookSeriesRef{}
	for rows.Next() {
		var ref models.BookSeriesRef
		var pgID pgtype.UUID
		if err := rows.Scan(&pgID, &ref.SeriesName, &ref.Position); err != nil {
			return nil, err
		}
		ref.SeriesID = uuid.UUID(pgID.Bytes)
		out = append(out, &ref)
	}
	return out, rows.Err()
}

func (r *SeriesRepo) RemoveBook(ctx context.Context, seriesID, bookID uuid.UUID) error {
	result, err := r.db.Exec(ctx,
		`DELETE FROM book_series WHERE series_id = $1 AND book_id = $2`,
		seriesID, bookID,
	)
	if err != nil {
		return fmt.Errorf("removing series book: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Scanners ─────────────────────────────────────────────────────────────────

func scanSeries(s scanner) (*models.Series, error) {
	var (
		pgID          pgtype.UUID
		pgLibraryID   pgtype.UUID
		pgTotal       pgtype.Int4
		pgOrigLang    pgtype.Text
		pgPubYear     pgtype.Int4
		pgDemographic pgtype.Text
		genres        []string
		pgLastDate    pgtype.Date
		pgNextDate    pgtype.Date
		previewJSON   []byte
		tagsJSON      []byte
		pgRating      pgtype.Int4
		pgMyRating    pgtype.Int4
		ser           models.Series
	)
	err := s.Scan(
		&pgID, &pgLibraryID, &ser.Name, &ser.Description,
		&pgTotal, &ser.Status, &pgOrigLang, &pgPubYear,
		&pgDemographic, &genres, &ser.URL,
		&ser.ExternalID, &ser.ExternalSource,
		&pgLastDate, &pgNextDate,
		&ser.BookCount,
		&ser.ArcCount,
		&ser.ReadCount, &ser.ReadingCount,
		&pgRating, &ser.RatedBooks, &pgMyRating,
		&previewJSON,
		&ser.CreatedAt, &ser.UpdatedAt,
		&tagsJSON,
	)
	if err != nil {
		return nil, err
	}
	ser.ID = uuid.UUID(pgID.Bytes)
	ser.LibraryID = uuid.UUID(pgLibraryID.Bytes)
	// Nil rather than nought when nothing is rated. A run nobody has an opinion
	// on is not a run everybody hated.
	if pgRating.Valid {
		v := int(pgRating.Int32)
		ser.Rating = &v
	}
	if pgMyRating.Valid {
		v := int(pgMyRating.Int32)
		ser.MyRating = &v
	}
	if pgTotal.Valid {
		v := int(pgTotal.Int32)
		ser.TotalCount = &v
	}
	if pgOrigLang.Valid {
		ser.OriginalLanguage = pgOrigLang.String
	}
	if pgPubYear.Valid {
		v := int(pgPubYear.Int32)
		ser.PublicationYear = &v
	}
	if pgDemographic.Valid {
		ser.Demographic = pgDemographic.String
	}
	if genres != nil {
		ser.Genres = genres
	} else {
		ser.Genres = []string{}
	}
	if pgLastDate.Valid {
		t := pgLastDate.Time
		ser.LastReleaseDate = &t
	}
	if pgNextDate.Valid {
		t := pgNextDate.Time
		ser.NextReleaseDate = &t
	}
	if err := json.Unmarshal(tagsJSON, &ser.Tags); err != nil || ser.Tags == nil {
		ser.Tags = []*models.Tag{}
	}
	if err := json.Unmarshal(previewJSON, &ser.PreviewBooks); err != nil || ser.PreviewBooks == nil {
		ser.PreviewBooks = []models.SeriesPreviewBook{}
	}
	return &ser, nil
}

func scanSeriesEntry(s scanner) (*models.SeriesEntry, error) {
	var (
		pgBookID     pgtype.UUID
		pgArcID      pgtype.UUID
		contribsJSON []byte
		entry        models.SeriesEntry
	)
	if err := s.Scan(
		&entry.Position,
		&entry.PositionEnd,
		&pgBookID, &entry.Title, &entry.Subtitle, &entry.MediaType,
		&pgArcID,
		&entry.UpdatedAt,
		&entry.HasCover,
		&entry.Held,
		&entry.UserReadStatus,
		&contribsJSON,
	); err != nil {
		return nil, err
	}
	entry.BookID = uuid.UUID(pgBookID.Bytes)
	if pgArcID.Valid {
		id := uuid.UUID(pgArcID.Bytes)
		entry.ArcID = &id
	}
	if err := json.Unmarshal(contribsJSON, &entry.Contributors); err != nil {
		entry.Contributors = nil
	}
	return &entry, nil
}
