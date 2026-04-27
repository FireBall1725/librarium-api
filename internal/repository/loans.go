// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LoanRepo struct {
	db *pgxpool.Pool
}

func NewLoanRepo(db *pgxpool.Pool) *LoanRepo {
	return &LoanRepo{db: db}
}

// loanSelect is the shared SELECT body used by every loan read. Loans
// no longer carry tags — the join was dropped along with `loan_tags`.
const loanSelect = `
	SELECT l.id, l.library_id, l.book_id, b.title,
	       l.loaned_to, l.loaned_at, l.due_date, l.returned_at,
	       COALESCE(l.notes, ''),
	       l.created_at, l.updated_at
	FROM loans l
	JOIN books b ON b.id = l.book_id
`

func (r *LoanRepo) List(ctx context.Context, libraryID uuid.UUID, includeReturned bool, search string, bookID uuid.UUID) ([]*models.Loan, error) {
	args := []any{libraryID, includeReturned}
	where := `WHERE l.library_id = $1 AND ($2 OR l.returned_at IS NULL)`
	if search != "" {
		args = append(args, "%"+search+"%")
		where += fmt.Sprintf(` AND lower(l.loaned_to || ' ' || b.title) LIKE lower($%d)`, len(args))
	}
	if bookID != uuid.Nil {
		args = append(args, bookID)
		where += fmt.Sprintf(` AND l.book_id = $%d`, len(args))
	}

	q := loanSelect + where + `
		ORDER BY l.loaned_at DESC, l.created_at DESC`

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing loans: %w", err)
	}
	defer rows.Close()

	var out []*models.Loan
	for rows.Next() {
		l, err := scanLoan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListActiveByBook returns every active (not yet returned) loan for the
// given book across every library. Powers the active-loan panel on the
// library-agnostic GetBook response.
func (r *LoanRepo) ListActiveByBook(ctx context.Context, bookID uuid.UUID) ([]*models.Loan, error) {
	q := loanSelect + `WHERE l.book_id = $1 AND l.returned_at IS NULL
		ORDER BY l.loaned_at DESC, l.created_at DESC`
	rows, err := r.db.Query(ctx, q, bookID)
	if err != nil {
		return nil, fmt.Errorf("listing active loans by book: %w", err)
	}
	defer rows.Close()
	var out []*models.Loan
	for rows.Next() {
		l, err := scanLoan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *LoanRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Loan, error) {
	q := loanSelect + `WHERE l.id = $1`
	l, err := scanLoan(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding loan: %w", err)
	}
	return l, nil
}

func (r *LoanRepo) Create(ctx context.Context, id, libraryID, bookID, createdBy uuid.UUID, loanedTo, notes string, loanedAt time.Time, dueDate *time.Time) (*models.Loan, error) {
	const q = `
		INSERT INTO loans (id, library_id, book_id, loaned_to, loaned_at, due_date, notes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7,''), $8)`
	if _, err := r.db.Exec(ctx, q, id, libraryID, bookID, loanedTo, loanedAt, dueDate, notes, createdBy); err != nil {
		return nil, fmt.Errorf("inserting loan: %w", err)
	}
	return r.FindByID(ctx, id)
}

func (r *LoanRepo) Update(ctx context.Context, id uuid.UUID, loanedTo, notes string, dueDate, returnedAt *time.Time) (*models.Loan, error) {
	const q = `
		UPDATE loans
		SET loaned_to   = $2,
		    due_date    = $3,
		    returned_at = $4,
		    notes       = NULLIF($5, ''),
		    updated_at  = NOW()
		WHERE id = $1`
	result, err := r.db.Exec(ctx, q, id, loanedTo, dueDate, returnedAt, notes)
	if err != nil {
		return nil, fmt.Errorf("updating loan: %w", err)
	}
	if result.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.FindByID(ctx, id)
}

func (r *LoanRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `DELETE FROM loans WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting loan: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// scanLoan scans a single loan row from any of the loan reads. Loans
// no longer carry tags; the loanSelect SELECT shape is the same for
// every read path.
func scanLoan(s scanner) (*models.Loan, error) {
	var (
		pgID        pgtype.UUID
		pgLibraryID pgtype.UUID
		pgBookID    pgtype.UUID
		pgDueDate   pgtype.Date
		pgReturned  pgtype.Date
		l           models.Loan
	)
	err := s.Scan(
		&pgID, &pgLibraryID, &pgBookID, &l.BookTitle,
		&l.LoanedTo, &l.LoanedAt, &pgDueDate, &pgReturned,
		&l.Notes, &l.CreatedAt, &l.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	l.ID = uuid.UUID(pgID.Bytes)
	l.LibraryID = uuid.UUID(pgLibraryID.Bytes)
	l.BookID = uuid.UUID(pgBookID.Bytes)
	if pgDueDate.Valid {
		t := pgDueDate.Time
		l.DueDate = &t
	}
	if pgReturned.Valid {
		t := pgReturned.Time
		l.ReturnedAt = &t
	}
	return &l, nil
}
