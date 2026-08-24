// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrSmartListNotEnumerable is returned when something tries to put a book in a
// smart list by hand. A smart list computes its membership from a filter, so
// adding to it would produce a row the filter does not agree with.
var ErrSmartListNotEnumerable = errors.New("a smart list computes its own contents")

// FilterVersionCurrent is the shape this release writes and understands.
//
// A stored filter is a query language with no schema. Unversioned, it gets
// silently reinterpreted the moment the filter vocabulary changes, which is the
// persistent form of a bug this project has already had transiently: the facet
// rail counted fields the list ignored, and a count disagreeing with its own
// rows was the only tell. A reader that meets a version it does not know must
// refuse rather than guess.
const FilterVersionCurrent = 1

type ListRepo struct {
	db *pgxpool.Pool
}

func NewListRepo(db *pgxpool.Pool) *ListRepo {
	return &ListRepo{db: db}
}

const listColumns = `
	l.id, l.owner_user_id, l.name, l.description, l.icon, l.color,
	l.kind, l.filter, l.filter_version, l.layout, l.display_order,
	l.visibility, l.shared_library_id, COALESCE(l.share_token, ''),
	(SELECT count(*) FROM list_books lb WHERE lb.list_id = l.id),
	l.created_at, l.updated_at`

func scanList(row pgx.Row) (*models.List, error) {
	var l models.List
	err := row.Scan(
		&l.ID, &l.OwnerUserID, &l.Name, &l.Description, &l.Icon, &l.Color,
		&l.Kind, &l.Filter, &l.FilterVersion, &l.Layout, &l.DisplayOrder,
		&l.Visibility, &l.SharedLibraryID, &l.ShareToken,
		&l.BookCount, &l.CreatedAt, &l.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// ListForUser returns the lists a person can see: their own, plus anything
// shared into a library they hold a role on.
//
// Sharing is resolved here rather than by the caller because a shared list must
// show only the books its viewer could already see, and that rule is easy to
// forget one call site at a time.
func (r *ListRepo) ListForUser(ctx context.Context, userID uuid.UUID) ([]*models.List, error) {
	q := `SELECT ` + listColumns + `
	        FROM lists l
	       WHERE l.owner_user_id = $1
	          OR (l.visibility = 'library'
	              AND EXISTS (SELECT 1 FROM user_roles ur
	                           WHERE ur.user_id = $1 AND ur.library_id = l.shared_library_id))
	       ORDER BY l.display_order, l.name`

	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("listing lists: %w", err)
	}
	defer rows.Close()

	out := make([]*models.List, 0)
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning list: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *ListRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.List, error) {
	q := `SELECT ` + listColumns + ` FROM lists l WHERE l.id = $1`
	l, err := scanList(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding list: %w", err)
	}
	return l, nil
}

// FindByShareToken resolves a public link. Only public lists resolve: a token
// left over from a list that has since been made private is not a way in.
func (r *ListRepo) FindByShareToken(ctx context.Context, token string) (*models.List, error) {
	q := `SELECT ` + listColumns + ` FROM lists l
	       WHERE l.share_token = $1 AND l.visibility = 'public'`

	l, err := scanList(r.db.QueryRow(ctx, q, token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding shared list: %w", err)
	}
	return l, nil
}

// CreateListInput describes a new list. Kind decides which half of the struct
// matters: a manual list ignores Filter, a smart one requires it.
type CreateListInput struct {
	OwnerUserID     uuid.UUID
	Name            string
	Description     string
	Icon            string
	Color           string
	Kind            string
	Filter          []byte
	Layout          string
	Visibility      string
	SharedLibraryID *uuid.UUID
}

func (r *ListRepo) Create(ctx context.Context, in CreateListInput) (*models.List, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, fmt.Errorf("list name is required")
	}
	if in.Kind != "manual" && in.Kind != "smart" {
		return nil, fmt.Errorf("list kind must be manual or smart")
	}
	if in.Kind == "smart" && len(in.Filter) == 0 {
		return nil, fmt.Errorf("a smart list needs a filter")
	}
	if in.Kind == "manual" {
		in.Filter = nil
	}
	if in.Layout == "" {
		in.Layout = "grid"
	}
	if in.Visibility == "" {
		in.Visibility = "private"
	}

	// The database enforces that these three agree, so getting them right here
	// is about returning a useful error rather than a constraint violation.
	var token any
	switch in.Visibility {
	case "private":
		in.SharedLibraryID = nil
	case "library":
		if in.SharedLibraryID == nil {
			return nil, fmt.Errorf("a list shared with a library needs the library")
		}
	case "public":
		in.SharedLibraryID = nil
		t, err := newShareToken()
		if err != nil {
			return nil, err
		}
		token = t
	default:
		return nil, fmt.Errorf("unknown visibility %q", in.Visibility)
	}

	var filterVersion any
	if in.Kind == "smart" {
		filterVersion = FilterVersionCurrent
	}

	q := `
		INSERT INTO lists (owner_user_id, name, description, icon, color, kind,
		                   filter, filter_version, layout, visibility,
		                   shared_library_id, share_token)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id`

	var id uuid.UUID
	err := r.db.QueryRow(ctx, q,
		in.OwnerUserID, in.Name, in.Description, in.Icon, in.Color, in.Kind,
		in.Filter, filterVersion, in.Layout, in.Visibility,
		in.SharedLibraryID, token).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("creating list: %w", err)
	}
	return r.FindByID(ctx, id)
}

// Delete removes a list and, by cascade, its membership rows.
func (r *ListRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM lists WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting list: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AddBook puts a work in a manual list.
func (r *ListRepo) AddBook(ctx context.Context, listID, bookID uuid.UUID, position float64) error {
	kind, err := r.kindOf(ctx, listID)
	if err != nil {
		return err
	}
	if kind == "smart" {
		return ErrSmartListNotEnumerable
	}

	const q = `
		INSERT INTO list_books (list_id, book_id, position)
		VALUES ($1, $2, $3)
		ON CONFLICT (list_id, book_id) DO UPDATE SET position = EXCLUDED.position`

	if _, err := r.db.Exec(ctx, q, listID, bookID, position); err != nil {
		return fmt.Errorf("adding book to list: %w", err)
	}
	return nil
}

// RemoveBook takes a work out of a manual list. Removing something that is not
// there is not an error.
func (r *ListRepo) RemoveBook(ctx context.Context, listID, bookID uuid.UUID) error {
	kind, err := r.kindOf(ctx, listID)
	if err != nil {
		return err
	}
	if kind == "smart" {
		return ErrSmartListNotEnumerable
	}

	if _, err := r.db.Exec(ctx,
		`DELETE FROM list_books WHERE list_id = $1 AND book_id = $2`, listID, bookID); err != nil {
		return fmt.Errorf("removing book from list: %w", err)
	}
	return nil
}

// BookIDs returns a manual list's contents in order. A smart list's contents
// come from running its filter, which is the caller's job.
func (r *ListRepo) BookIDs(ctx context.Context, listID uuid.UUID) ([]uuid.UUID, error) {
	const q = `SELECT book_id FROM list_books WHERE list_id = $1 ORDER BY position, added_at`

	rows, err := r.db.Query(ctx, q, listID)
	if err != nil {
		return nil, fmt.Errorf("listing list contents: %w", err)
	}
	defer rows.Close()

	out := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning list book: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *ListRepo) kindOf(ctx context.Context, id uuid.UUID) (string, error) {
	var kind string
	err := r.db.QueryRow(ctx, `SELECT kind FROM lists WHERE id = $1`, id).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("reading list kind: %w", err)
	}
	return kind, nil
}

// newShareToken mints an unguessable public link. A share link is the only way
// into a list without an account, so the token is the credential and 32 bytes
// of crypto/rand is what makes guessing it pointless.
func newShareToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating share token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
