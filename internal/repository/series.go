// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

// ─── Series CRUD ──────────────────────────────────────────────────────────────

func (r *SeriesRepo) List(ctx context.Context, libraryID uuid.UUID, search, tagFilter string) ([]*models.Series, error) {
	args := []any{libraryID}
	where := `WHERE s.library_id = $1`
	if search != "" {
		args = append(args, "%"+search+"%")
		where += fmt.Sprintf(` AND lower(s.name) LIKE lower($%d)`, len(args))
	}
	if tagFilter != "" {
		args = append(args, tagFilter)
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM series_tags st JOIN tags t ON t.id = st.tag_id WHERE st.series_id = s.id AND lower(t.name) = lower($%d))`, len(args))
	}

	q := `
		SELECT s.id, s.library_id, s.name, COALESCE(s.description,''),
		       s.total_count, s.status, s.original_language, s.publication_year,
		       s.demographic, s.genres, COALESCE(s.url,''),
		       COALESCE(s.external_id,''), COALESCE(s.external_source,''),
		       (SELECT MAX(sv.release_date) FROM series_volumes sv WHERE sv.series_id = s.id AND sv.release_date <= CURRENT_DATE) AS last_release_date,
		       (SELECT MIN(sv.release_date) FROM series_volumes sv WHERE sv.series_id = s.id AND sv.release_date > CURRENT_DATE) AS next_release_date,
		       COUNT(bs.book_id) AS book_count,
		       s.created_at, s.updated_at,
		       ` + seriesTagsSubquery + ` AS tags
		FROM series s
		LEFT JOIN book_series bs ON bs.series_id = s.id
		` + where + `
		GROUP BY s.id
		ORDER BY s.name`

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

func (r *SeriesRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Series, error) {
	q := `
		SELECT s.id, s.library_id, s.name, COALESCE(s.description,''),
		       s.total_count, s.status, s.original_language, s.publication_year,
		       s.demographic, s.genres, COALESCE(s.url,''),
		       COALESCE(s.external_id,''), COALESCE(s.external_source,''),
		       (SELECT MAX(sv.release_date) FROM series_volumes sv WHERE sv.series_id = s.id AND sv.release_date <= CURRENT_DATE) AS last_release_date,
		       (SELECT MIN(sv.release_date) FROM series_volumes sv WHERE sv.series_id = s.id AND sv.release_date > CURRENT_DATE) AS next_release_date,
		       COUNT(bs.book_id) AS book_count,
		       s.created_at, s.updated_at,
		       ` + seriesTagsSubquery + ` AS tags
		FROM series s
		LEFT JOIN book_series bs ON bs.series_id = s.id
		WHERE s.id = $1
		GROUP BY s.id`
	s, err := scanSeries(r.db.QueryRow(ctx, q, id))
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
	return r.FindByID(ctx, id)
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
	return r.FindByID(ctx, id)
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

func (r *SeriesRepo) ListBooks(ctx context.Context, seriesID uuid.UUID) ([]*models.SeriesEntry, error) {
	const q = `
		SELECT
			bs.position,
			b.id, b.title, COALESCE(b.subtitle,''), mt.display_name,
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
		WHERE bs.series_id = $1
		ORDER BY bs.position`
	rows, err := r.db.Query(ctx, q, seriesID)
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
		JOIN library_books lb ON lb.book_id = b.id
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
			c            MatchCandidate
			pgBookID     pgtype.UUID
			otherJSON    []byte
			otherParsed  []struct {
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
		JOIN library_books lb ON lb.book_id = b.id
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
		tagsJSON      []byte
		ser           models.Series
	)
	err := s.Scan(
		&pgID, &pgLibraryID, &ser.Name, &ser.Description,
		&pgTotal, &ser.Status, &pgOrigLang, &pgPubYear,
		&pgDemographic, &genres, &ser.URL,
		&ser.ExternalID, &ser.ExternalSource,
		&pgLastDate, &pgNextDate,
		&ser.BookCount,
		&ser.CreatedAt, &ser.UpdatedAt,
		&tagsJSON,
	)
	if err != nil {
		return nil, err
	}
	ser.ID = uuid.UUID(pgID.Bytes)
	ser.LibraryID = uuid.UUID(pgLibraryID.Bytes)
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
	return &ser, nil
}

func scanSeriesEntry(s scanner) (*models.SeriesEntry, error) {
	var (
		pgBookID     pgtype.UUID
		contribsJSON []byte
		entry        models.SeriesEntry
	)
	if err := s.Scan(
		&entry.Position,
		&pgBookID, &entry.Title, &entry.Subtitle, &entry.MediaType,
		&contribsJSON,
	); err != nil {
		return nil, err
	}
	entry.BookID = uuid.UUID(pgBookID.Bytes)
	if err := json.Unmarshal(contribsJSON, &entry.Contributors); err != nil {
		entry.Contributors = nil
	}
	return &entry, nil
}
