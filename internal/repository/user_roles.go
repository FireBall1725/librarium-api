// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrRoleScopeMismatch is returned when a library-scoped role is granted
// instance-wide, or an instance role pinned to one library.
//
// Enforced by a composite foreign key to roles (id, scope). Granting
// library_viewer across the whole instance is meaningless; granting
// instance_admin on one library would hand out admin:users permissions scoped
// to a library, which is not a thing.
var ErrRoleScopeMismatch = errors.New("that role cannot be granted at that scope")

// ErrGrantExists is returned when someone already holds that role there.
var ErrGrantExists = errors.New("that role is already granted")

// RoleGrant is one person holding one role, either on a library or everywhere.
type RoleGrant struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Username  string
	RoleID    uuid.UUID
	RoleCode  string
	Scope     string
	LibraryID *uuid.UUID
	GrantedBy *uuid.UUID
	GrantedAt string
}

type UserRoleRepo struct {
	db *pgxpool.Pool
}

func NewUserRoleRepo(db *pgxpool.Pool) *UserRoleRepo {
	return &UserRoleRepo{db: db}
}

// ReadableLibraryIDs returns the libraries a person can see.
//
// This is the scope every list query narrows to, and the one place that
// decision is made. An instance-wide grant of books:read reaches every library,
// which is expressed here in data rather than as a bypass inside the middleware
// so that "who can see this" has a single answer that can be queried.
func (r *UserRoleRepo) ReadableLibraryIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	const q = `
		SELECT l.id
		  FROM libraries l
		 WHERE (EXISTS (SELECT 1 FROM user_roles ur
		                 WHERE ur.user_id = $1 AND ur.library_id = l.id)
		     OR EXISTS (SELECT 1 FROM user_roles ur
		                  JOIN role_permissions rp ON rp.role_id = ur.role_id
		                  JOIN permissions p ON p.id = rp.permission_id
		                 WHERE ur.user_id = $1 AND ur.library_id IS NULL
		                   AND p.name = 'books:read'))
		 ORDER BY l.name`

	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("listing readable libraries: %w", err)
	}
	defer rows.Close()

	out := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning library id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// HasPermission reports whether someone may do a thing in a library.
//
// A grant with a null library_id applies everywhere, so an instance admin
// passes without a per-library row. libraryID may be uuid.Nil to ask about an
// instance-wide permission such as admin:users:update.
func (r *UserRoleRepo) HasPermission(ctx context.Context, userID, libraryID uuid.UUID, permission string) (bool, error) {
	const q = `
		SELECT EXISTS (
		    SELECT 1
		      FROM user_roles ur
		      JOIN role_permissions rp ON rp.role_id = ur.role_id
		      JOIN permissions p ON p.id = rp.permission_id
		     WHERE ur.user_id = $1
		       AND p.name = $3
		       AND (ur.library_id IS NULL OR ur.library_id = $2))`

	var ok bool
	var lib any
	if libraryID != uuid.Nil {
		lib = libraryID
	}
	if err := r.db.QueryRow(ctx, q, userID, lib, permission).Scan(&ok); err != nil {
		return false, fmt.Errorf("checking permission: %w", err)
	}
	return ok, nil
}

// ListForLibrary returns who holds what on one library. This is the members
// list: membership IS holding a library-scoped role, so there is nothing else
// to join.
func (r *UserRoleRepo) ListForLibrary(ctx context.Context, libraryID uuid.UUID) ([]*RoleGrant, error) {
	const q = `
		SELECT ur.id, ur.user_id, u.username, ur.role_id, ro.code, ur.scope,
		       ur.library_id, ur.granted_by, ur.granted_at::text
		  FROM user_roles ur
		  JOIN users u ON u.id = ur.user_id
		  JOIN roles ro ON ro.id = ur.role_id
		 WHERE ur.library_id = $1
		 ORDER BY u.username`

	return r.collect(ctx, q, libraryID)
}

// ListForUser returns everything one person holds, across every scope.
func (r *UserRoleRepo) ListForUser(ctx context.Context, userID uuid.UUID) ([]*RoleGrant, error) {
	const q = `
		SELECT ur.id, ur.user_id, u.username, ur.role_id, ro.code, ur.scope,
		       ur.library_id, ur.granted_by, ur.granted_at::text
		  FROM user_roles ur
		  JOIN users u ON u.id = ur.user_id
		  JOIN roles ro ON ro.id = ur.role_id
		 WHERE ur.user_id = $1
		 ORDER BY ur.scope, ro.code`

	return r.collect(ctx, q, userID)
}

func (r *UserRoleRepo) collect(ctx context.Context, q string, arg uuid.UUID) ([]*RoleGrant, error) {
	rows, err := r.db.Query(ctx, q, arg)
	if err != nil {
		return nil, fmt.Errorf("listing role grants: %w", err)
	}
	defer rows.Close()

	out := make([]*RoleGrant, 0)
	for rows.Next() {
		var g RoleGrant
		if err := rows.Scan(&g.ID, &g.UserID, &g.Username, &g.RoleID, &g.RoleCode,
			&g.Scope, &g.LibraryID, &g.GrantedBy, &g.GrantedAt); err != nil {
			return nil, fmt.Errorf("scanning role grant: %w", err)
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

// Grant gives someone a role. Pass a nil libraryID for an instance-wide grant.
//
// The scope is read from the role rather than taken from the caller, so the
// composite foreign key has something consistent to check and a caller cannot
// smuggle a library role into instance scope by asking for it.
func (r *UserRoleRepo) Grant(ctx context.Context, userID, roleID uuid.UUID, libraryID *uuid.UUID, grantedBy *uuid.UUID) error {
	var scope string
	err := r.db.QueryRow(ctx, `SELECT scope FROM roles WHERE id = $1`, roleID).Scan(&scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("reading role scope: %w", err)
	}

	// Say this plainly here rather than letting the CHECK produce a constraint
	// name: the two mistakes are different and the messages should be too.
	switch {
	case scope == "library" && libraryID == nil:
		return ErrRoleScopeMismatch
	case scope == "instance" && libraryID != nil:
		return ErrRoleScopeMismatch
	}

	const q = `
		INSERT INTO user_roles (user_id, role_id, scope, library_id, granted_by)
		VALUES ($1, $2, $3, $4, $5)`

	_, err = r.db.Exec(ctx, q, userID, roleID, scope, libraryID, grantedBy)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "23505":
			return ErrGrantExists
		case pgErr.Code == "23503" && strings.Contains(pgErr.ConstraintName, "role_scope"):
			return ErrRoleScopeMismatch
		case pgErr.Code == "23503":
			return ErrNotFound
		case pgErr.Code == "23514":
			return ErrRoleScopeMismatch
		}
	}
	if err != nil {
		return fmt.Errorf("granting role: %w", err)
	}
	return nil
}

// Revoke removes a grant. Revoking one that is not there is not an error.
func (r *UserRoleRepo) Revoke(ctx context.Context, userID, roleID uuid.UUID, libraryID *uuid.UUID) error {
	const q = `
		DELETE FROM user_roles
		 WHERE user_id = $1 AND role_id = $2
		   AND library_id IS NOT DISTINCT FROM $3`

	if _, err := r.db.Exec(ctx, q, userID, roleID, libraryID); err != nil {
		return fmt.Errorf("revoking role: %w", err)
	}
	return nil
}

// RevokeAllInLibrary removes someone from a library entirely, which is what
// "remove this member" means now that membership is a role grant.
func (r *UserRoleRepo) RevokeAllInLibrary(ctx context.Context, userID, libraryID uuid.UUID) error {
	const q = `DELETE FROM user_roles WHERE user_id = $1 AND library_id = $2`
	tag, err := r.db.Exec(ctx, q, userID, libraryID)
	if err != nil {
		return fmt.Errorf("removing library member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// IsInstanceAdmin reports whether someone holds an instance-scoped role that
// can manage users.
//
// This replaces the users.is_instance_admin boolean. A flag beside an
// instance_admin role is two mechanisms for one fact and they can disagree;
// asking the role is the single answer.
func (r *UserRoleRepo) IsInstanceAdmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	return r.HasPermission(ctx, userID, uuid.Nil, "admin:users:update")
}
