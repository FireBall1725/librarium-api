// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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

// BuiltinList is a smart list this release ships with.
//
// These were saved views the browser seeded for itself, which raced: sign in on
// a phone and a laptop the same afternoon and each seeded its own copy. The
// server owns them now, and they are smart lists because that is what they
// always were, a name over a filter.
//
// Filter is written in the versioned shape FilterVersionCurrent describes, so a
// built-in is not a special case to any reader.
type BuiltinList struct {
	Key          string
	Name         string
	Query        string
	Layout       string
	Icon         string
	DisplayOrder int
	// Hidden keeps a list out of the rail. Default is what the books page opens
	// on rather than something to click, and there has to be something to open
	// on, which is also why it is Permanent.
	Hidden    bool
	Permanent bool
}

var BuiltinLists = []BuiltinList{
	{Key: "default", Name: "Default", Query: "", Layout: "list", DisplayOrder: 0, Hidden: true, Permanent: true},
	{Key: "reading", Name: "Reading now", Query: "status=reading", Layout: "grid", Icon: "next", DisplayOrder: 1},
	{Key: "unread", Name: "Up next", Query: "status=unread", Layout: "grid", DisplayOrder: 2},
	{Key: "read", Name: "Finished", Query: "status=read", Layout: "list", DisplayOrder: 3},
	{Key: "favourites", Name: "Favourites", Query: "fav=true", Layout: "grid", Icon: "star", DisplayOrder: 4},
	{Key: "five-stars", Name: "Five stars", Query: "rating=5", Layout: "grid", DisplayOrder: 5},
	{Key: "signed", Name: "Signed copies", Query: "tag=signed", Layout: "list", DisplayOrder: 6},
}

var builtinByKey = func() map[string]BuiltinList {
	m := make(map[string]BuiltinList, len(BuiltinLists))
	for _, b := range BuiltinLists {
		m[b.Key] = b
	}
	return m
}()

// ErrListPermanent is returned when a caller tries to delete a list that has to
// exist. Handlers map it to 409, so a client showing a delete control on one
// finds out rather than the delete quietly doing nothing.
var ErrListPermanent = errors.New("list cannot be deleted")

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
	COALESCE(l.builtin_key, ''), l.created_at, l.updated_at`

func scanList(row pgx.Row) (*models.List, error) {
	var l models.List
	err := row.Scan(
		&l.ID, &l.OwnerUserID, &l.Name, &l.Description, &l.Icon, &l.Color,
		&l.Kind, &l.Filter, &l.FilterVersion, &l.Layout, &l.DisplayOrder,
		&l.Visibility, &l.SharedLibraryID, &l.ShareToken,
		&l.BookCount, &l.BuiltinKey, &l.CreatedAt, &l.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if b, ok := builtinByKey[l.BuiltinKey]; ok {
		l.Hidden, l.Permanent = b.Hidden, b.Permanent
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

// SeedBuiltIns gives a user the lists this release ships with.
//
// Safe to call on every read, which is the point: "has this user been seeded"
// has no honest answer once deleting a built-in is allowed, so there is no flag
// to check and the insert carries its own idempotency through
// lists_builtin_key_idx. A built-in a user deleted stays deleted only until the
// next seed, and that is a real limitation worth naming rather than a bug: the
// alternative is a tombstone table for something nobody has asked to do yet.
func (r *ListRepo) SeedBuiltIns(ctx context.Context, userID uuid.UUID) error {
	for _, b := range BuiltinLists {
		filter, err := json.Marshal(map[string]string{"query": b.Query})
		if err != nil {
			return fmt.Errorf("encoding built-in filter %q: %w", b.Key, err)
		}
		_, err = r.db.Exec(ctx, `
			INSERT INTO lists (owner_user_id, name, icon, kind, filter, filter_version,
			                   layout, display_order, visibility, builtin_key)
			VALUES ($1, $2, $3, 'smart', $4, $5, $6, $7, 'private', $8)
			ON CONFLICT DO NOTHING`,
			userID, b.Name, b.Icon, filter, FilterVersionCurrent,
			b.Layout, b.DisplayOrder, b.Key)
		if err != nil {
			return fmt.Errorf("seeding built-in list %q: %w", b.Key, err)
		}
	}
	return nil
}

// UpdateListInput carries only what a person may change. Kind, owner and
// builtin_key are absent on purpose: a manual list cannot become smart without
// its enumerated books becoming a lie, and a list cannot start claiming to be
// something this release shipped.
type UpdateListInput struct {
	Name         *string
	Description  *string
	Icon         *string
	Color        *string
	Layout       *string
	DisplayOrder *int
	Filter       []byte
}

// Update changes a list in place. A nil field is left alone rather than
// cleared, so a client can send one key without having to send the row back.
func (r *ListRepo) Update(ctx context.Context, id uuid.UUID, in UpdateListInput) (*models.List, error) {
	kind, err := r.kindOf(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(in.Filter) > 0 && kind != "smart" {
		return nil, ErrSmartListNotEnumerable
	}
	if in.Name != nil {
		if *in.Name = strings.TrimSpace(*in.Name); *in.Name == "" {
			return nil, fmt.Errorf("list name is required")
		}
	}
	if in.Layout != nil && !validLayouts[*in.Layout] {
		return nil, fmt.Errorf("unknown layout %q", *in.Layout)
	}

	// COALESCE rather than a built-up SET list: every column is named once, the
	// query is a constant, and a nil argument means "keep" without the argument
	// positions shifting under it.
	q := `
		UPDATE lists
		SET name          = COALESCE($2, name),
		    description   = COALESCE($3, description),
		    icon          = COALESCE($4, icon),
		    color         = COALESCE($5, color),
		    layout        = COALESCE($6, layout),
		    display_order = COALESCE($7, display_order),
		    filter        = COALESCE($8, filter),
		    updated_at    = NOW()
		WHERE id = $1`
	var filter any
	if len(in.Filter) > 0 {
		filter = in.Filter
	}
	tag, err := r.db.Exec(ctx, q, id, in.Name, in.Description, in.Icon,
		in.Color, in.Layout, in.DisplayOrder, filter)
	if err != nil {
		return nil, fmt.Errorf("updating list: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.FindByID(ctx, id)
}

var validLayouts = map[string]bool{"grid": true, "list": true, "compact": true}

// Delete removes a list and, by cascade, its membership rows.
func (r *ListRepo) Delete(ctx context.Context, id uuid.UUID) error {
	var key *string
	if err := r.db.QueryRow(ctx,
		`SELECT builtin_key FROM lists WHERE id = $1`, id).Scan(&key); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("finding list: %w", err)
	}
	if key != nil && builtinByKey[*key].Permanent {
		return ErrListPermanent
	}
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

// ContainingBook returns the caller's lists that hold a book.
//
// The reverse of BookIDs, and the one a book page asks: "which of my lists is
// this on". Manual lists only, because a smart list's membership is whatever
// its filter matches right now and answering for one would mean running every
// stored filter to draw a label.
//
// Scoped to the caller's own lists plus anything shared into a library they can
// reach, the same visibility rule the filter and the facet follow.
func (r *ListRepo) ContainingBook(ctx context.Context, userID, bookID uuid.UUID) ([]*models.List, error) {
	q := `
		SELECT ` + listColumns + `
		  FROM lists l
		  JOIN list_books lb ON lb.list_id = l.id AND lb.book_id = $2
		 WHERE l.kind = 'manual'
		   AND (l.owner_user_id = $1
		        OR (l.visibility = 'library' AND EXISTS (
		              SELECT 1 FROM user_permissions up
		               WHERE up.user_id = $1
		                 AND (up.library_id IS NULL OR up.library_id = l.shared_library_id))))
		 ORDER BY l.display_order, l.name`

	rows, err := r.db.Query(ctx, q, userID, bookID)
	if err != nil {
		return nil, fmt.Errorf("listing the lists holding a book: %w", err)
	}
	defer rows.Close()

	// Initialised rather than nil: a nil slice marshals to null and the React
	// client crashes where it expects an array.
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
