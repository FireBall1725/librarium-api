// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package middleware

import (
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Routes addressed by a thing's own id (/copies/{id}, /contributors/{id})
// carry no library in the path, so RequireLibraryPermission can't guard them.
// These did nothing: any signed-in user could edit or delete another
// library's copies and places, and a scoped token wasn't held to its scopes.
// RequireOn finds the libraries the thing belongs to and asks the same
// question RequireLibraryPermission asks, in any of them.

// Scope says which libraries a request's thing belongs to: a query returning
// library ids, with $2 bound to the path value named Param.
type Scope struct {
	Param string
	SQL   string
}

// The scopes the id-addressed routes need.
var (
	ScopeCopy     = Scope{"copy_id", `SELECT library_id FROM copies WHERE id = $2`}
	ScopeLocation = Scope{"location_id", `SELECT library_id FROM copy_locations WHERE id = $2`}
	// A book belongs to every library that holds a copy of it. held_books,
	// not library_books: that table stopped being written at the tiers
	// migration, so a book added since isn't in it.
	ScopeBook    = Scope{"book_id", `SELECT library_id FROM held_books WHERE book_id = $2`}
	ScopeEdition = Scope{"edition_id", `
		SELECT hb.library_id FROM held_books hb
		  JOIN book_editions e ON e.book_id = hb.book_id WHERE e.id = $2`}
	// Contributors are shared by every library: holding the permission in
	// any of them is enough.
	ScopeAnyLibrary = Scope{}
)

// RequireOn checks that the caller holds permission in a library the thing
// belongs to, and that their token scope allows it. Instance admins pass the
// library check, but not the scope check. Must be chained after RequireAuth.
func RequireOn(db *pgxpool.Pool, permission string, scope Scope) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFromContext(r.Context())
			if claims == nil {
				respond.Error(w, http.StatusUnauthorized, "authentication required")
				return
			}
			if !claims.ScopeAllows(permission) {
				respond.Error(w, http.StatusForbidden, "token scope does not permit this action")
				return
			}
			if claims.IsInstanceAdmin {
				next.ServeHTTP(w, r)
				return
			}

			where := "TRUE"
			args := []any{claims.UserID, nil, permission}
			if scope.Param != "" {
				id, err := uuid.Parse(r.PathValue(scope.Param))
				if err != nil {
					respond.Error(w, http.StatusBadRequest, "invalid "+scope.Param)
					return
				}
				args[1] = id
				where = "(ur.library_id IS NULL OR ur.library_id IN (" + scope.SQL + "))"
			}
			q := `
				SELECT EXISTS (
				    SELECT 1
				      FROM user_roles ur
				      JOIN role_permissions rp ON rp.role_id = ur.role_id
				      JOIN permissions p       ON p.id = rp.permission_id
				     WHERE ur.user_id = $1
				       AND p.name     = $3
				       AND ` + where + `)`
			if scope.Param == "" {
				// $2 is unused; keep the numbering by referencing it harmlessly.
				q += ` AND $2::uuid IS NULL`
			}

			var allowed bool
			if err := db.QueryRow(r.Context(), q, args...).Scan(&allowed); err != nil {
				respond.Error(w, http.StatusInternalServerError, "permission check failed")
				return
			}
			if !allowed {
				respond.Error(w, http.StatusForbidden, "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
