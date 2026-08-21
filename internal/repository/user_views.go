// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserView is one saved set of filters belonging to a user.
//
// Ids are the browser's own strings rather than UUIDs: the built-ins are named
// and user-made ones were minted client-side, so keeping them makes the
// one-time import an insert rather than a remapping.
type UserView struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Params       string  `json:"params"`
	Layout       string  `json:"layout"`
	Icon         *string `json:"icon,omitempty"`
	BuiltIn      bool    `json:"built_in,omitempty"`
	Hidden       bool    `json:"hidden,omitempty"`
	Permanent    bool    `json:"permanent,omitempty"`
	DisplayOrder int     `json:"display_order"`
}

type UserViewRepo struct {
	db *pgxpool.Pool
}

func NewUserViewRepo(db *pgxpool.Pool) *UserViewRepo {
	return &UserViewRepo{db: db}
}

// BuiltInViews ship with the product and are seeded the first time a user asks
// for their views.
//
// This list lived in the client. It is here because the server now owns views,
// and because a client that seeded its own would race a second client: sign in
// on a phone and a laptop the same afternoon and each would insert its own copy.
var BuiltInViews = []UserView{
	// Hidden and permanent: it is what Books opens on rather than a row in the
	// rail, and there has to be something to open on.
	{ID: "default", Name: "Default", Params: "", Layout: "rows", BuiltIn: true, Hidden: true, Permanent: true, DisplayOrder: 0},
	{ID: "reading", Name: "Reading now", Params: "status=reading", Layout: "grid", BuiltIn: true, DisplayOrder: 1, Icon: ptr("next")},
	{ID: "unread", Name: "Up next", Params: "status=unread", Layout: "grid", BuiltIn: true, DisplayOrder: 2},
	{ID: "read", Name: "Finished", Params: "status=read", Layout: "rows", BuiltIn: true, DisplayOrder: 3},
	{ID: "favourites", Name: "Favourites", Params: "fav=true", Layout: "grid", BuiltIn: true, DisplayOrder: 4, Icon: ptr("star")},
	{ID: "five-stars", Name: "Five stars", Params: "rating=5", Layout: "grid", BuiltIn: true, DisplayOrder: 5},
	{ID: "signed", Name: "Signed copies", Params: "tag=signed", Layout: "rows", BuiltIn: true, DisplayOrder: 6},
}

func ptr(s string) *string { return &s }

const userViewColumns = `id, name, params, layout, icon, built_in, hidden, permanent, display_order`

// List returns the user's views, seeding the built-ins the first time.
//
// "No rows" is what means "never seeded", which works because the Default is
// permanent and cannot be deleted: once a user has been seeded they always have
// at least it. That saves carrying a separate seeded flag, and it is the same
// reasoning the client used, where deleting every built-in had to not look like
// a first run.
func (r *UserViewRepo) List(ctx context.Context, userID uuid.UUID) ([]UserView, error) {
	views, err := r.list(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(views) > 0 {
		return views, nil
	}
	if err := r.seed(ctx, userID); err != nil {
		return nil, err
	}
	return r.list(ctx, userID)
}

func (r *UserViewRepo) list(ctx context.Context, userID uuid.UUID) ([]UserView, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+userViewColumns+` FROM user_views WHERE user_id = $1
		 ORDER BY display_order, created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("listing views: %w", err)
	}
	defer rows.Close()

	out := make([]UserView, 0, len(BuiltInViews))
	for rows.Next() {
		var v UserView
		if err := rows.Scan(&v.ID, &v.Name, &v.Params, &v.Layout, &v.Icon,
			&v.BuiltIn, &v.Hidden, &v.Permanent, &v.DisplayOrder); err != nil {
			return nil, fmt.Errorf("scanning view: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *UserViewRepo) seed(ctx context.Context, userID uuid.UUID) error {
	for _, v := range BuiltInViews {
		if err := r.Upsert(ctx, userID, v); err != nil {
			return fmt.Errorf("seeding built-in view %q: %w", v.ID, err)
		}
	}
	return nil
}

// Upsert writes a view, replacing one with the same id.
//
// ON CONFLICT rather than a separate create and update: the import from the
// browser, seeding, and an ordinary save are the same operation, and a client
// retrying a save should not fail because the row landed the first time.
func (r *UserViewRepo) Upsert(ctx context.Context, userID uuid.UUID, v UserView) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_views (user_id, id, name, params, layout, icon, built_in, hidden, permanent, display_order)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (user_id, id) DO UPDATE
		SET name = EXCLUDED.name,
		    params = EXCLUDED.params,
		    layout = EXCLUDED.layout,
		    icon = EXCLUDED.icon,
		    -- built_in, hidden and permanent are properties of the view we
		    -- shipped, not of the edit. A rename must not be able to make the
		    -- Default deletable, or a user's own view claim to be a built-in.
		    display_order = EXCLUDED.display_order,
		    updated_at = NOW()`,
		userID, v.ID, v.Name, v.Params, v.Layout, v.Icon,
		v.BuiltIn, v.Hidden, v.Permanent, v.DisplayOrder)
	if err != nil {
		return fmt.Errorf("saving view: %w", err)
	}
	return nil
}

// Delete removes a view. Permanent views are refused rather than silently kept,
// so a client showing a Delete control on one finds out.
func (r *UserViewRepo) Delete(ctx context.Context, userID uuid.UUID, id string) error {
	var permanent bool
	err := r.db.QueryRow(ctx,
		`SELECT permanent FROM user_views WHERE user_id = $1 AND id = $2`, userID, id).Scan(&permanent)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("finding view: %w", err)
	}
	if permanent {
		return ErrViewPermanent
	}
	_, err = r.db.Exec(ctx, `DELETE FROM user_views WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return fmt.Errorf("deleting view: %w", err)
	}
	return nil
}

// ErrViewPermanent is returned when a caller tries to delete a view that has to
// exist. Handlers map it to 409.
var ErrViewPermanent = errors.New("view cannot be deleted")
