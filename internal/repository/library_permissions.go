// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// LibraryAccess is one library the caller can reach, with the permissions they
// effectively hold in it.
//
// Clients need this per library rather than per request because a single list
// can mix libraries: a user may own one and have been invited to another as a
// viewer. Gating a whole screen on one role is only correct while the screen
// shows one library, which stops being true the moment books from several are
// listed together. Without this a client either fires a permission check per
// row or guesses, and guessing means rendering controls the API will reject.
type LibraryAccess struct {
	LibraryID   uuid.UUID `json:"library_id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Role        string    `json:"role"`
	Permissions []string  `json:"permissions"`
	BookCount   int       `json:"book_count"`
}

// ListAccessForUser returns every library the user can reach and the permission
// names their role grants there.
//
// Instance admins are handled separately: RequireLibraryPermission lets them
// past the membership check entirely, so reporting only their memberships would
// under-report what they can actually do and the client would hide controls
// that would in fact succeed.
func (r *LibraryRepo) ListAccessForUser(ctx context.Context, userID uuid.UUID, isInstanceAdmin bool) ([]*LibraryAccess, error) {
	// `lm.deleted_at IS NULL` is defensive. Membership removal is a hard DELETE
	// today so the column is never set, but if that ever becomes a soft delete
	// an unfiltered permission query would keep granting access to people who
	// had been removed.
	const memberQ = `
		SELECT l.id, l.name, l.slug, ro.name AS role,
		       COALESCE(array_agg(DISTINCT p.name) FILTER (WHERE p.name IS NOT NULL), '{}') AS perms,
		       (SELECT COUNT(*) FROM library_books lb
		         WHERE lb.library_id = l.id AND lb.deleted_at IS NULL) AS book_count
		FROM library_memberships lm
		JOIN libraries l         ON l.id = lm.library_id
		JOIN roles ro            ON ro.id = lm.role_id
		LEFT JOIN role_permissions rp ON rp.role_id = lm.role_id
		LEFT JOIN permissions p       ON p.id = rp.permission_id
		WHERE lm.user_id = $1 AND lm.deleted_at IS NULL
		GROUP BY l.id, l.name, l.slug, ro.name
		ORDER BY l.name`

	// Every library, every permission: what an instance admin can actually do.
	const adminQ = `
		SELECT l.id, l.name, l.slug, 'instance_admin' AS role,
		       (SELECT COALESCE(array_agg(name ORDER BY name), '{}') FROM permissions) AS perms,
		       (SELECT COUNT(*) FROM library_books lb
		         WHERE lb.library_id = l.id AND lb.deleted_at IS NULL) AS book_count
		FROM libraries l
		ORDER BY l.name`

	q := memberQ
	args := []any{userID}
	if isInstanceAdmin {
		q = adminQ
		args = nil
	}

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing library access: %w", err)
	}
	defer rows.Close()

	out := make([]*LibraryAccess, 0)
	for rows.Next() {
		a := &LibraryAccess{}
		if err := rows.Scan(&a.LibraryID, &a.Name, &a.Slug, &a.Role, &a.Permissions, &a.BookCount); err != nil {
			return nil, fmt.Errorf("scanning library access: %w", err)
		}
		if a.Permissions == nil {
			a.Permissions = []string{}
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading library access: %w", err)
	}
	return out, nil
}
