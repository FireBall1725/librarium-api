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

			// Reads user_roles, where membership IS holding a library-scoped
			// role, so being a member and having a role stopped being two facts
			// that could disagree.
			//
			// The library_id IS NULL arm is an instance-wide grant, which
			// reaches every library. That makes the IsInstanceAdmin bypass above
			// redundant rather than load-bearing: migration 000025 gave every
			// instance admin a matching grant. The bypass stays until the
			// contract migration drops users.is_instance_admin, because removing
			// a safety check in the same release that introduces its replacement
			// is how a permission bug ships unnoticed.
			const q = `
				SELECT EXISTS (
				    SELECT 1
				      FROM user_roles ur
				      JOIN role_permissions rp ON rp.role_id = ur.role_id
				      JOIN permissions p       ON p.id = rp.permission_id
				     WHERE ur.user_id = $2
				       AND p.name     = $3
				       AND (ur.library_id IS NULL OR ur.library_id = $1))`

			var allowed bool
			if err := db.QueryRow(r.Context(), q, libraryID, claims.UserID, permission).Scan(&allowed); err != nil {
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
