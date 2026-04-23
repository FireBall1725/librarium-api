// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ContributorListOpts controls filtering, sorting, and pagination for ListForLibraryPaged.
type ContributorListOpts struct {
	Search  string
	Letter  string
	Sort    string // "name" | "sort_name" | "book_count"
	SortDir string // "asc" | "desc"
	Page    int
	PerPage int
}

type ContributorRepo struct {
	db *pgxpool.Pool
}

func NewContributorRepo(db *pgxpool.Pool) *ContributorRepo {
	return &ContributorRepo{db: db}
}

// ─── Full contributor columns ─────────────────────────────────────────────────
// All queries using contributorCols must alias the contributors table as "c".

const contributorCols = `c.id, c.name, COALESCE(c.sort_name,''), c.is_corporate,
	COALESCE(c.bio,''), c.born_date, c.died_date,
	COALESCE(c.nationality,''), c.external_ids, c.created_at, c.updated_at,
	EXISTS(SELECT 1 FROM cover_images ci WHERE ci.entity_type='contributor' AND ci.entity_id=c.id AND ci.is_primary=true)`

func scanFullContributor(s scanner) (*models.Contributor, error) {
	var (
		pgID        pgtype.UUID
		c           models.Contributor
		bornDate    pgtype.Date
		diedDate    pgtype.Date
		externalRaw []byte
	)
	if err := s.Scan(
		&pgID, &c.Name, &c.SortName, &c.IsCorporate, &c.Bio,
		&bornDate, &diedDate,
		&c.Nationality, &externalRaw,
		&c.CreatedAt, &c.UpdatedAt,
		&c.HasPhoto,
	); err != nil {
		return nil, err
	}
	c.ID = uuid.UUID(pgID.Bytes)
	if bornDate.Valid {
		t := bornDate.Time
		c.BornDate = &t
	}
	if diedDate.Valid {
		t := diedDate.Time
		c.DiedDate = &t
	}
	if len(externalRaw) > 0 {
		_ = json.Unmarshal(externalRaw, &c.ExternalIDs)
	}
	if c.ExternalIDs == nil {
		c.ExternalIDs = map[string]string{}
	}
	return &c, nil
}

// ─── Queries ──────────────────────────────────────────────────────────────────

func (r *ContributorRepo) Search(ctx context.Context, query string, limit int) ([]*models.Contributor, error) {
	q := `SELECT ` + contributorCols + `
		FROM contributors c
		WHERE c.name ILIKE '%' || $1 || '%'
		ORDER BY c.name
		LIMIT $2`

	rows, err := r.db.Query(ctx, q, query, limit)
	if err != nil {
		return nil, fmt.Errorf("searching contributors: %w", err)
	}
	defer rows.Close()

	var out []*models.Contributor
	for rows.Next() {
		c, err := scanFullContributor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListForLibrary returns all contributors who have at least one book in the given
// library, ordered by name. BookCount is set to the number of distinct books in
// that library for each contributor.
func (r *ContributorRepo) ListForLibrary(ctx context.Context, libraryID uuid.UUID) ([]*models.Contributor, error) {
	q := `SELECT ` + contributorCols + `, COUNT(DISTINCT b.id)::int AS book_count
		FROM contributors c
		JOIN book_contributors bc ON bc.contributor_id = c.id
		JOIN books b ON b.id = bc.book_id
		JOIN library_books lb ON lb.book_id = b.id
		WHERE lb.library_id = $1
		GROUP BY c.id
		ORDER BY c.name`

	rows, err := r.db.Query(ctx, q, libraryID)
	if err != nil {
		return nil, fmt.Errorf("listing contributors for library: %w", err)
	}
	defer rows.Close()

	var out []*models.Contributor
	for rows.Next() {
		c, err := scanContributorWithBookCount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListForLibraryPaged returns a paginated, filtered, sorted slice of contributors
// who have at least one book in the given library.
func (r *ContributorRepo) ListForLibraryPaged(ctx context.Context, libraryID uuid.UUID, opts ContributorListOpts) ([]*models.Contributor, int, error) {
	conditions := []string{"lb.library_id = $1"}
	args := []any{libraryID}
	idx := 2

	if opts.Search != "" {
		conditions = append(conditions, fmt.Sprintf("c.name ILIKE '%%' || $%d || '%%'", idx))
		args = append(args, opts.Search)
		idx++
	}
	if opts.Letter != "" {
		conditions = append(conditions, fmt.Sprintf("upper(left(c.name,1)) = $%d", idx))
		args = append(args, strings.ToUpper(opts.Letter))
		idx++
	}
	where := "WHERE " + strings.Join(conditions, " AND ")

	// Count.
	var total int
	countQ := `SELECT COUNT(DISTINCT c.id)
		FROM contributors c
		JOIN book_contributors bc ON bc.contributor_id = c.id
		JOIN books b ON b.id = bc.book_id
		JOIN library_books lb ON lb.book_id = b.id
		` + where
	if err := r.db.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting contributors: %w", err)
	}
	if total == 0 {
		return []*models.Contributor{}, 0, nil
	}

	sortCol := "c.name"
	switch opts.Sort {
	case "book_count":
		sortCol = "book_count"
	case "sort_name":
		sortCol = "lower(COALESCE(NULLIF(c.sort_name,''), c.name))"
	}
	dir := "ASC"
	if strings.EqualFold(opts.SortDir, "desc") {
		dir = "DESC"
	}
	secondary := ""
	if sortCol != "c.name" {
		secondary = ", c.name ASC"
	}

	if opts.PerPage <= 0 {
		opts.PerPage = 25
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}
	offset := (opts.Page - 1) * opts.PerPage

	dataQ := `SELECT ` + contributorCols + `, COUNT(DISTINCT b.id)::int AS book_count
		FROM contributors c
		JOIN book_contributors bc ON bc.contributor_id = c.id
		JOIN books b ON b.id = bc.book_id
		JOIN library_books lb ON lb.book_id = b.id
		` + where + `
		GROUP BY c.id
		ORDER BY ` + sortCol + ` ` + dir + secondary + `
		LIMIT $` + fmt.Sprintf("%d", idx) + ` OFFSET $` + fmt.Sprintf("%d", idx+1)
	args = append(args, opts.PerPage, offset)

	rows, err := r.db.Query(ctx, dataQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing contributors paged: %w", err)
	}
	defer rows.Close()

	var out []*models.Contributor
	for rows.Next() {
		c, err := scanContributorWithBookCount(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// LettersForLibrary returns the distinct uppercased first letters of contributor
// names that have books in the given library (A-Z only).
func (r *ContributorRepo) LettersForLibrary(ctx context.Context, libraryID uuid.UUID) ([]string, error) {
	q := `SELECT DISTINCT upper(left(c.name,1)) AS letter
		FROM contributors c
		JOIN book_contributors bc ON bc.contributor_id = c.id
		JOIN books b ON b.id = bc.book_id
		JOIN library_books lb ON lb.book_id = b.id
		WHERE lb.library_id = $1
		  AND c.name ~ '^[A-Za-z]'
		ORDER BY letter`
	rows, err := r.db.Query(ctx, q, libraryID)
	if err != nil {
		return nil, fmt.Errorf("getting contributor letters: %w", err)
	}
	defer rows.Close()
	var letters []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		letters = append(letters, l)
	}
	return letters, rows.Err()
}

// Delete hard-deletes a contributor. Returns ErrInUse if any book_contributors
// rows still reference the contributor (across all libraries).
func (r *ContributorRepo) Delete(ctx context.Context, id uuid.UUID) error {
	var count int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM book_contributors WHERE contributor_id = $1`, id,
	).Scan(&count); err != nil {
		return fmt.Errorf("checking contributor references: %w", err)
	}
	if count > 0 {
		return ErrInUse
	}
	tag, err := r.db.Exec(ctx, `DELETE FROM contributors WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting contributor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateName renames a contributor.
func (r *ContributorRepo) UpdateName(ctx context.Context, id uuid.UUID, name string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE contributors SET name=$2, updated_at=NOW() WHERE id=$1`, id, name)
	if err != nil {
		return fmt.Errorf("renaming contributor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// scanContributorWithBookCount scans contributorCols + book_count.
func scanContributorWithBookCount(s scanner) (*models.Contributor, error) {
	var (
		pgID        pgtype.UUID
		c           models.Contributor
		bornDate    pgtype.Date
		diedDate    pgtype.Date
		externalRaw []byte
	)
	if err := s.Scan(
		&pgID, &c.Name, &c.SortName, &c.IsCorporate, &c.Bio,
		&bornDate, &diedDate,
		&c.Nationality, &externalRaw,
		&c.CreatedAt, &c.UpdatedAt,
		&c.HasPhoto,
		&c.BookCount,
	); err != nil {
		return nil, err
	}
	c.ID = uuid.UUID(pgID.Bytes)
	if bornDate.Valid {
		t := bornDate.Time
		c.BornDate = &t
	}
	if diedDate.Valid {
		t := diedDate.Time
		c.DiedDate = &t
	}
	if len(externalRaw) > 0 {
		_ = json.Unmarshal(externalRaw, &c.ExternalIDs)
	}
	if c.ExternalIDs == nil {
		c.ExternalIDs = map[string]string{}
	}
	return &c, nil
}

func (r *ContributorRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Contributor, error) {
	q := `SELECT ` + contributorCols + ` FROM contributors c WHERE c.id = $1`
	c, err := scanFullContributor(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding contributor: %w", err)
	}
	return c, nil
}

func (r *ContributorRepo) Create(ctx context.Context, id uuid.UUID, name, sortName string, isCorporate bool) (*models.Contributor, error) {
	_, err := r.db.Exec(ctx,
		`INSERT INTO contributors (id, name, sort_name, is_corporate) VALUES ($1, $2, $3, $4)`,
		id, name, sortName, isCorporate,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting contributor: %w", err)
	}
	return r.FindByID(ctx, id)
}

// UpdateProfile persists name, sort_name, is_corporate, bio, birth/death dates, and nationality for a contributor.
func (r *ContributorRepo) UpdateProfile(ctx context.Context, c *models.Contributor) error {
	extJSON, err := json.Marshal(c.ExternalIDs)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `
		UPDATE contributors
		SET name=$2, sort_name=$3, is_corporate=$4, bio=$5, born_date=$6, died_date=$7,
		    nationality=$8, external_ids=$9, updated_at=NOW()
		WHERE id=$1`,
		c.ID, c.Name, c.SortName, c.IsCorporate, c.Bio, c.BornDate, c.DiedDate, c.Nationality, extJSON,
	)
	return err
}

// ListMissingSortName returns contributors whose sort_name is empty.
// Used by the backfill on startup after the 000003 migration adds the column.
func (r *ContributorRepo) ListMissingSortName(ctx context.Context) ([]*models.Contributor, error) {
	q := `SELECT ` + contributorCols + ` FROM contributors c WHERE c.sort_name = '' OR c.sort_name IS NULL`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("listing contributors missing sort_name: %w", err)
	}
	defer rows.Close()
	var out []*models.Contributor
	for rows.Next() {
		c, err := scanFullContributor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetSortName updates only the sort_name column (used by backfill).
func (r *ContributorRepo) SetSortName(ctx context.Context, id uuid.UUID, sortName string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE contributors SET sort_name=$2, updated_at=NOW() WHERE id=$1`,
		id, sortName,
	)
	return err
}

// Update persists bio, birth/death dates, nationality, and external_ids for a contributor.
func (r *ContributorRepo) Update(ctx context.Context, c *models.Contributor) error {
	extJSON, err := json.Marshal(c.ExternalIDs)
	if err != nil {
		return err
	}
	var bornDate, diedDate *time.Time
	if c.BornDate != nil {
		bornDate = c.BornDate
	}
	if c.DiedDate != nil {
		diedDate = c.DiedDate
	}
	_, err = r.db.Exec(ctx, `
		UPDATE contributors
		SET bio=$2, born_date=$3, died_date=$4, nationality=$5, external_ids=$6, updated_at=NOW()
		WHERE id=$1`,
		c.ID, c.Bio, bornDate, diedDate, c.Nationality, extJSON,
	)
	return err
}

// ─── Works ────────────────────────────────────────────────────────────────────

// ListWorks returns non-deleted works for a contributor, annotated with
// whether each work exists in the given library (matched by ISBN).
// Pass uuid.Nil for libraryID to skip the in-library check.
func (r *ContributorRepo) ListWorks(ctx context.Context, contributorID, libraryID uuid.UUID) ([]*models.ContributorWork, error) {
	var q string
	var args []any

	if libraryID == uuid.Nil {
		q = `
			SELECT cw.id, cw.contributor_id, cw.title, COALESCE(cw.isbn_13,''),
			       COALESCE(cw.isbn_10,''), cw.publish_year, COALESCE(cw.cover_url,''),
			       cw.source, cw.deleted_at, cw.created_at,
			       false, NULL::uuid
			FROM contributor_works cw
			WHERE cw.contributor_id = $1 AND cw.deleted_at IS NULL
			ORDER BY cw.publish_year ASC NULLS LAST, cw.title`
		args = []any{contributorID}
	} else {
		q = `
			SELECT cw.id, cw.contributor_id, cw.title, COALESCE(cw.isbn_13,''),
			       COALESCE(cw.isbn_10,''), cw.publish_year, COALESCE(cw.cover_url,''),
			       cw.source, cw.deleted_at, cw.created_at,
			       (inlib.id IS NOT NULL), inlib.id
			FROM contributor_works cw
			LEFT JOIN LATERAL (
			    SELECT b.id
			    FROM books b
			    JOIN book_editions be ON be.book_id = b.id
			    JOIN library_books lbj ON lbj.book_id = b.id
			    WHERE lbj.library_id = $2
			      AND (
			           (cw.isbn_13 <> '' AND be.isbn_13 = cw.isbn_13) OR
			           (cw.isbn_10 <> '' AND be.isbn_10 = cw.isbn_10)
			      )
			    LIMIT 1
			) inlib ON TRUE
			WHERE cw.contributor_id = $1 AND cw.deleted_at IS NULL
			ORDER BY cw.publish_year ASC NULLS LAST, cw.title`
		args = []any{contributorID, libraryID}
	}

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing contributor works: %w", err)
	}
	defer rows.Close()

	var out []*models.ContributorWork
	for rows.Next() {
		w, err := scanWork(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func scanWork(s scanner) (*models.ContributorWork, error) {
	var (
		pgID        pgtype.UUID
		pgCID       pgtype.UUID
		pgLibBookID pgtype.UUID
		w           models.ContributorWork
		publishYear pgtype.Int4
	)
	if err := s.Scan(
		&pgID, &pgCID, &w.Title, &w.ISBN13, &w.ISBN10,
		&publishYear, &w.CoverURL, &w.Source, &w.DeletedAt, &w.CreatedAt,
		&w.InLibrary, &pgLibBookID,
	); err != nil {
		return nil, err
	}
	w.ID = uuid.UUID(pgID.Bytes)
	w.ContributorID = uuid.UUID(pgCID.Bytes)
	if publishYear.Valid {
		y := int(publishYear.Int32)
		w.PublishYear = &y
	}
	if pgLibBookID.Valid {
		id := uuid.UUID(pgLibBookID.Bytes)
		w.LibraryBookID = &id
	}
	return &w, nil
}

// UpsertWorks replaces all non-deleted works from a given source for a contributor,
// then inserts the new set. Works from other sources are untouched.
func (r *ContributorRepo) UpsertWorks(ctx context.Context, contributorID uuid.UUID, source string, works []*models.ContributorWork) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Hard-delete previous entries from this source (not user-deleted ones — those
	// were already soft-deleted and should stay gone even after re-enriching).
	if _, err := tx.Exec(ctx,
		`DELETE FROM contributor_works WHERE contributor_id=$1 AND source=$2 AND deleted_at IS NULL`,
		contributorID, source,
	); err != nil {
		return err
	}

	for _, w := range works {
		if _, err := tx.Exec(ctx, `
			INSERT INTO contributor_works (id, contributor_id, title, isbn_13, isbn_10, publish_year, cover_url, source)
			VALUES ($1, $2, $3, NULLIF($4,''), NULLIF($5,''), $6, NULLIF($7,''), $8)`,
			uuid.New(), contributorID, w.Title,
			w.ISBN13, w.ISBN10, w.PublishYear,
			w.CoverURL, source,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// DeleteWork soft-deletes a single work by ID.
func (r *ContributorRepo) DeleteWork(ctx context.Context, workID uuid.UUID) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE contributor_works SET deleted_at=NOW() WHERE id=$1 AND deleted_at IS NULL`,
		workID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
