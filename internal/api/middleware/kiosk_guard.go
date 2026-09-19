// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package middleware

import (
	"net/http"
	"strings"

	"github.com/fireball1725/librarium-api/internal/api/respond"
)

// A kiosk's tokens sit on a wall-mounted iPad, so they get less than their
// scopes alone would allow. Routes behind RequireLibraryPermission already
// check scopes; plenty of routes behind plain login do their own checks, or
// none, and would otherwise let a kiosk edit a copy or read the personal
// data of the admin who registered it.

// KioskGuard is for routes behind plain login. A kiosk's own token may read,
// but not anyone's /me or /auth/me data; a member's kiosk session may read.
// Neither may write. Other callers pass straight through.
func KioskGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims == nil || claims.Kiosk == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !readOnly(r.Method) {
			respond.Error(w, http.StatusForbidden, "a kiosk can't do that")
			return
		}
		if claims.Kiosk == "device" && personal(r.URL.Path) {
			respond.Error(w, http.StatusForbidden, "a kiosk can't do that")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// KioskMemberWrites is for the few personal routes a member signed in on a
// kiosk may write to, such as adding a book to their own list. A kiosk's own
// token is kept out entirely; everyone else passes through.
func KioskMemberWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims != nil && claims.Kiosk == "device" {
			respond.Error(w, http.StatusForbidden, "a kiosk can't do that")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func readOnly(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func personal(path string) bool {
	return strings.HasPrefix(path, "/api/v1/me/") || path == "/api/v1/me" ||
		strings.HasPrefix(path, "/api/v1/auth/me")
}
