// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func guarded(g func(http.Handler) http.Handler, kiosk, method, path string) int {
	h := g(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	req := httptest.NewRequest(method, path, nil)
	req = req.WithContext(context.WithValue(req.Context(), claimsKey, &UserClaims{FromToken: kiosk != "", Kiosk: kiosk}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// A kiosk's own token renamed and deleted a place through /locations, which
// only checks for a login. The guard keeps kiosk tokens to reading there.
func TestKioskGuard(t *testing.T) {
	cases := []struct {
		kiosk, method, path string
		want                int
	}{
		{"", "PATCH", "/api/v1/locations/x", 200}, // everyone else is untouched
		{"", "GET", "/api/v1/me/books", 200},
		{"device", "PATCH", "/api/v1/locations/x", 403}, // the hole
		{"device", "DELETE", "/api/v1/copies/x", 403},
		{"device", "GET", "/api/v1/books/x", 200},  // the catalogue is fine
		{"device", "GET", "/api/v1/me/books", 403}, // the registering admin's own data isn't
		{"device", "GET", "/api/v1/auth/me/preferences", 403},
		{"member", "GET", "/api/v1/me/loans", 200}, // a member reads their own
		{"member", "PUT", "/api/v1/books/x/me", 403},
		{"member", "POST", "/api/v1/lookup/upc/learn", 403},
	}
	for _, c := range cases {
		if got := guarded(KioskGuard, c.kiosk, c.method, c.path); got != c.want {
			t.Errorf("%q %s %s = %d, want %d", c.kiosk, c.method, c.path, got, c.want)
		}
	}
}

func TestKioskMemberWrites(t *testing.T) {
	if got := guarded(KioskMemberWrites, "member", "POST", "/api/v1/me/lists/l/books/b"); got != 200 {
		t.Errorf("member adding to their list = %d", got)
	}
	if got := guarded(KioskMemberWrites, "device", "POST", "/api/v1/me/lists/l/books/b"); got != 403 {
		t.Errorf("kiosk adding to the admin's list = %d", got)
	}
	if got := guarded(KioskMemberWrites, "", "POST", "/api/v1/me/lists/l/books/b"); got != 200 {
		t.Errorf("a normal login = %d", got)
	}
}
