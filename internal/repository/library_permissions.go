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
	// Permissions come from user_permissions rather than from the row's own
	// role. library_members collapses a person to one role per library, which is
	// right for a label and wrong for this: a grant table can hold two roles, and
	// an instance-wide grant reaches every library without appearing as a
	// membership at all. Reading the one role would under-report both, and this
	// answer decides which controls the client shows, so under-reporting hides
	// controls that would in fact succeed.
	//
	// `lm.deleted_at IS NULL` is defensive. Removal is a hard DELETE today so
	// the column is never set, but if that ever becomes a soft delete an
	// unfiltered permission query would keep granting access to people who had
	// been removed.
	const memberQ = `
		SELECT l.id, l.name, l.slug, ro.name AS role,
		       COALESCE((SELECT array_agg(DISTINCT up.permission_code)
		                   FROM user_permissions up
		                  WHERE up.user_id = $1
		                    AND (up.library_id IS NULL OR up.library_id = l.id)), '{}') AS perms,
		       (SELECT COUNT(*) FROM held_books lb
		         WHERE lb.library_id = l.id AND lb.deleted_at IS NULL) AS book_count
		FROM library_members lm
		JOIN libraries l ON l.id = lm.library_id
		JOIN roles ro    ON ro.id = lm.role_id
		WHERE lm.user_id = $1 AND lm.deleted_at IS NULL
		ORDER BY l.name`

	// Every library, every permission: what an instance admin can actually do.
	const adminQ = `
		SELECT l.id, l.name, l.slug, 'instance_admin' AS role,
		       (SELECT COALESCE(array_agg(name ORDER BY name), '{}') FROM permissions) AS perms,
		       (SELECT COUNT(*) FROM held_books lb
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

// CollectionCounts is what the navigation shows beside Books, Series and
// Authors.
type CollectionCounts struct {
	Books   int `json:"books"`
	Series  int `json:"series"`
	Authors int `json:"authors"`
	// Loans is what is still out, not every loan ever recorded. A nav count of
	// every loan in history would climb forever and never mean anything.
	Loans int `json:"loans"`
	// LoansOverdue is the subset worth reacting to, so the rail can say which
	// of those outstanding loans is late without a second request.
	LoansOverdue int `json:"loans_overdue"`
	// Suggestions is the caller's undismissed suggestions, which is what the
	// Suggestions page lists.
	//
	// Deliberately not the ownership facet's "suggested" tally. That one ranks
	// a book by the strongest claim on it, so a suggestion for something
	// already on the shelf counts as shelf and drops out — the facet answers
	// "how many books are only ever suggestions", which is a smaller number and
	// the wrong one to put beside a page listing all of them.
	Suggestions int `json:"suggestions"`
}

// CountsForLibraries totals the collection across a set of libraries.
//
// One round trip with a scalar subquery each rather than a query apiece: the
// navigation needs them all together on every page, and they are cheap counts
// against indexed columns.
//
// Books counts DISTINCT book ids, not junction rows. A work held by two
// libraries is one book on the shelf, and counting the junction would report a
// number the Books page then contradicts.
// callerID may be uuid.Nil, in which case Suggestions comes back 0: suggestions
// belong to a user rather than to a library, so there is nothing to count
// without one.
func (r *LibraryRepo) CountsForLibraries(ctx context.Context, libraryIDs []uuid.UUID, callerID uuid.UUID) (*CollectionCounts, error) {
	out := &CollectionCounts{}
	if len(libraryIDs) == 0 {
		return out, nil
	}

	args := []any{libraryIDs}
	suggestions := "0"
	if callerID != uuid.Nil {
		args = append(args, callerID)
		suggestions = fmt.Sprintf(
			`(SELECT COUNT(*) FROM ai_suggestions s WHERE s.user_id = $%d AND s.status = 'new')`,
			len(args))
	}

	q := `
SELECT
  (SELECT COUNT(DISTINCT lb.book_id) FROM held_books lb
    WHERE lb.library_id = ANY($1) AND lb.deleted_at IS NULL),
  (SELECT COUNT(*) FROM series s WHERE s.library_id = ANY($1)),
  (SELECT COUNT(DISTINCT bc.contributor_id)
     FROM book_contributors bc
     JOIN held_books lb2 ON lb2.book_id = bc.book_id AND lb2.deleted_at IS NULL
    WHERE lb2.library_id = ANY($1) AND bc.role = 'author'),
  (SELECT COUNT(*) FROM loans l
    WHERE l.library_id = ANY($1) AND l.returned_at IS NULL),
  (SELECT COUNT(*) FROM loans l
    WHERE l.library_id = ANY($1) AND l.returned_at IS NULL
      AND l.due_date IS NOT NULL AND l.due_date < CURRENT_DATE),
  ` + suggestions

	if err := r.db.QueryRow(ctx, q, args...).Scan(
		&out.Books, &out.Series, &out.Authors, &out.Loans, &out.LoansOverdue, &out.Suggestions,
	); err != nil {
		return nil, fmt.Errorf("counting collection: %w", err)
	}
	return out, nil
}
