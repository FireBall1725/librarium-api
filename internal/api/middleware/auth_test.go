// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package middleware

import "testing"

func TestScopeAllows(t *testing.T) {
	cases := []struct {
		name   string
		scopes []string
		perm   string
		want   bool
	}{
		{
			name:   "nil scopes = JWT or full-access PAT: everything allowed",
			scopes: nil,
			perm:   "books:read",
			want:   true,
		},
		{
			name:   "empty scopes = fail-closed (shouldn't occur in practice)",
			scopes: []string{},
			perm:   "books:read",
			want:   false,
		},
		{
			name:   "perm present",
			scopes: []string{"books:read", "loans:read"},
			perm:   "books:read",
			want:   true,
		},
		{
			name:   "perm missing",
			scopes: []string{"books:read", "loans:read"},
			perm:   "books:create",
			want:   false,
		},
		{
			name:   "instance:admin requires explicit grant",
			scopes: []string{"books:read"},
			perm:   "instance:admin",
			want:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			claims := &UserClaims{TokenScopes: c.scopes}
			if got := claims.ScopeAllows(c.perm); got != c.want {
				t.Errorf("ScopeAllows(%q) with scopes=%v = %v, want %v",
					c.perm, c.scopes, got, c.want)
			}
		})
	}
}
