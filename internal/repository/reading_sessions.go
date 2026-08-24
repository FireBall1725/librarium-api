// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrPageNeedsEdition is returned when progress is measured in pages without
// saying which printing. A page number is meaningless without knowing what it
// counts: page 200 of a mass-market paperback is not page 200 of the omnibus.
var ErrPageNeedsEdition = errors.New("page progress needs to say which edition")

// ProgressUnits is the closed set of ways to measure how far through something
// is. Typed rather than a jsonb blob, so progress can be compared, sorted and
// aggregated instead of every reader guessing at a shape nobody validates.
var ProgressUnits = []string{"page", "percent", "seconds"}

type ReadingSessionRepo struct {
	db *pgxpool.Pool
}

func NewReadingSessionRepo(db *pgxpool.Pool) *ReadingSessionRepo {
	return &ReadingSessionRepo{db: db}
}

const sessionColumns = `
	id, user_id, book_id, edition_id, started_at, finished_at, status,
	COALESCE(progress_unit, ''), progress_value, created_at`

func scanSession(row pgx.Row) (*models.ReadingSession, error) {
	var s models.ReadingSession
	err := row.Scan(
		&s.ID, &s.UserID, &s.BookID, &s.EditionID,
		&s.StartedAt, &s.FinishedAt, &s.Status,
		&s.ProgressUnit, &s.ProgressValue, &s.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ListForBook returns one person's passes through a work, most recent first.
//
// A reread is a second row rather than an overwrite, which is what reread_count
// was standing in for: a counter could say a book had been read three times but
// not when, or in which printing.
func (r *ReadingSessionRepo) ListForBook(ctx context.Context, userID, bookID uuid.UUID) ([]*models.ReadingSession, error) {
	q := `SELECT ` + sessionColumns + `
	        FROM reading_sessions
	       WHERE user_id = $1 AND book_id = $2
	       ORDER BY COALESCE(started_at, created_at) DESC, created_at DESC`

	rows, err := r.db.Query(ctx, q, userID, bookID)
	if err != nil {
		return nil, fmt.Errorf("listing reading sessions: %w", err)
	}
	defer rows.Close()

	out := make([]*models.ReadingSession, 0)
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning reading session: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *ReadingSessionRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.ReadingSession, error) {
	q := `SELECT ` + sessionColumns + ` FROM reading_sessions WHERE id = $1`
	s, err := scanSession(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding reading session: %w", err)
	}
	return s, nil
}

// CreateSessionInput describes one pass through a work. Everything is optional
// except who and what, because marking a book read without ever logging a
// session is the common case and this only records the people who want more.
type CreateSessionInput struct {
	UserID        uuid.UUID
	BookID        uuid.UUID
	EditionID     *uuid.UUID
	StartedAt     *string
	FinishedAt    *string
	Status        string
	ProgressUnit  string
	ProgressValue *float64
}

func (r *ReadingSessionRepo) Create(ctx context.Context, in CreateSessionInput) (*models.ReadingSession, error) {
	if in.Status == "" {
		in.Status = "reading"
	}
	if in.ProgressUnit == "page" && in.EditionID == nil {
		return nil, ErrPageNeedsEdition
	}

	const q = `
		INSERT INTO reading_sessions (user_id, book_id, edition_id, started_at, finished_at,
		                              status, progress_unit, progress_value)
		VALUES ($1, $2, $3, $4::timestamptz, $5::timestamptz, $6, $7, $8)
		RETURNING id`

	var id uuid.UUID
	err := r.db.QueryRow(ctx, q,
		in.UserID, in.BookID, in.EditionID, in.StartedAt, in.FinishedAt,
		in.Status, nullIfEmpty(in.ProgressUnit), in.ProgressValue).Scan(&id)
	if err != nil {
		return nil, translateSessionErr(err)
	}
	return r.FindByID(ctx, id)
}

// Finish closes a session, which is the common write: someone marks a book done
// and the session that was open becomes the record of when.
func (r *ReadingSessionRepo) Finish(ctx context.Context, id uuid.UUID, finishedAt *string, status string) (*models.ReadingSession, error) {
	if status == "" {
		status = "finished"
	}

	const q = `
		UPDATE reading_sessions
		   SET finished_at = COALESCE($2::timestamptz, NOW()), status = $3
		 WHERE id = $1`

	tag, err := r.db.Exec(ctx, q, id, finishedAt, status)
	if err != nil {
		return nil, translateSessionErr(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.FindByID(ctx, id)
}

func (r *ReadingSessionRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM reading_sessions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting reading session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func translateSessionErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "23503" && strings.Contains(pgErr.ConstraintName, "edition_matches_book"):
			return ErrEditionNotOfBook
		case pgErr.Code == "23503":
			return ErrNotFound
		case pgErr.Code == "23514" && strings.Contains(pgErr.ConstraintName, "page_needs_edition"):
			return ErrPageNeedsEdition
		}
	}
	return fmt.Errorf("writing reading session: %w", err)
}
