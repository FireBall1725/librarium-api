// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package middleware

import (
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RequireInstanceAdmin rejects requests from non-admin authenticated users.
// Must be chained after RequireAuth. Also rejects PAT-authenticated requests
// whose token scope doesn't include `instance:admin`, so an admin user's
// scoped token can't accidentally reach admin endpoints.
func RequireInstanceAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims == nil || !claims.IsInstanceAdmin {
			respond.Error(w, http.StatusForbidden, "instance admin access required")
			return
		}
		if !claims.ScopeAllows("instance:admin") {
			respond.Error(w, http.StatusForbidden, "token scope does not permit this action")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireLibraryPermission checks that the authenticated user holds the given
// permission in the library identified by the {library_id} path parameter.
// Instance admins bypass the library-role check, but the token-scope check
// still applies — a scope-capped PAT minted by an admin cannot exceed its
// scope even though the user could.
//
// Must be chained after RequireAuth.
func RequireLibraryPermission(db *pgxpool.Pool, permission string) func(http.Handler) http.Handler {
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

			libraryID := r.PathValue("library_id")
			if libraryID == "" {
				respond.Error(w, http.StatusBadRequest, "missing library_id path parameter")
				return
			}

			const q = `
				SELECT COUNT(*)
				FROM library_memberships lm
				JOIN role_permissions rp ON rp.role_id = lm.role_id
				JOIN permissions p      ON p.id = rp.permission_id
				WHERE lm.library_id = $1
				  AND lm.user_id    = $2
				  AND p.name        = $3`

			var count int
			if err := db.QueryRow(r.Context(), q, libraryID, claims.UserID, permission).Scan(&count); err != nil {
				respond.Error(w, http.StatusInternalServerError, "permission check failed")
				return
			}
			if count == 0 {
				respond.Error(w, http.StatusForbidden, "insufficient permissions")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
