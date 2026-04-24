// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package middleware

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/auth"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

type contextKey string

const claimsKey contextKey = "user_claims"

// UserClaims holds the decoded credential data attached to a request context.
// Populated by either the JWT branch or the personal-access-token branch of
// RequireAuth; downstream handlers and permission middleware don't care
// which.
//
// TokenScopes carries the scope cap for PAT-authenticated requests. A nil
// slice means "no token scope" — either the request came in with a JWT, or
// the PAT had an empty scopes array (inherit user's full permissions).
// A non-nil (possibly empty) slice means the caller is scope-capped: every
// permission check must AND-intersect against this list.
type UserClaims struct {
	JTI             uuid.UUID
	UserID          uuid.UUID
	IsInstanceAdmin bool
	ExpiresAt       time.Time
	TokenScopes     []string
	// FromToken is true when the request authenticated via a personal
	// access token rather than an interactive JWT. Distinguishes
	// full-access PATs (TokenScopes == nil) from real JWT sessions, which
	// would otherwise look identical in the claims. Use this to gate
	// actions that should only ever come from an interactive session —
	// e.g. minting new API tokens.
	FromToken bool
}

// ScopeAllows reports whether the caller's token scope permits the given
// permission string. JWT-authenticated callers (TokenScopes == nil) always
// pass. PAT-authenticated callers with an empty-but-non-nil scope list fail
// closed — that state shouldn't occur (empty scopes in the DB become nil
// here) but a fail-closed default is the safer code shape.
func (c *UserClaims) ScopeAllows(perm string) bool {
	if c.TokenScopes == nil {
		return true
	}
	return slices.Contains(c.TokenScopes, perm)
}

// RequireAuth validates a Bearer credential and attaches UserClaims.
// Accepts two credential shapes:
//
//   - JWT (default) — signed session token from the interactive login flow.
//     Validated, checked against the denylist, attached as claims.
//   - Personal access token (`lbrm_pat_...`) — long-lived credential minted
//     by the user from the web UI for scripted/machine access. Looked up by
//     sha256 in api_tokens, attached with TokenScopes for the RBAC layer to
//     enforce.
//
// Downstream middleware and handlers treat the two identically except for
// the scope enforcement in RequireLibraryPermission / RequireInstanceAdmin.
func RequireAuth(
	jwtSvc *auth.JWTService,
	denylist *repository.DenylistRepo,
	apiTokens *repository.APITokenRepo,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				respond.Error(w, http.StatusUnauthorized, "missing or invalid authorization header")
				return
			}
			tokenStr := strings.TrimPrefix(header, "Bearer ")

			if strings.HasPrefix(tokenStr, repository.APITokenPrefix) {
				claims, ok := authWithPAT(r.Context(), tokenStr, apiTokens)
				if !ok {
					respond.Error(w, http.StatusUnauthorized, "invalid or expired token")
					return
				}
				next.ServeHTTP(w, r.WithContext(
					context.WithValue(r.Context(), claimsKey, claims),
				))
				return
			}

			claims, err := jwtSvc.Validate(tokenStr)
			if err != nil {
				respond.Error(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}

			jti, err := uuid.Parse(claims.ID)
			if err != nil {
				respond.Error(w, http.StatusUnauthorized, "invalid token")
				return
			}

			revoked, err := denylist.IsRevoked(r.Context(), jti)
			if err != nil || revoked {
				respond.Error(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), claimsKey, &UserClaims{
				JTI:             jti,
				UserID:          claims.UserID,
				IsInstanceAdmin: claims.IsInstanceAdmin,
				ExpiresAt:       claims.ExpiresAt.Time,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// authWithPAT resolves an lbrm_pat_* bearer into UserClaims. Looks up the
// token by its sha256 hash, joins the users table for the admin flag, and
// fires a background TouchLastUsed so auth latency isn't blocked on the
// write. Returns (claims, true) on success; (nil, false) for any failure
// reason the caller surfaces as 401 without leaking a distinction.
func authWithPAT(ctx context.Context, raw string, apiTokens *repository.APITokenRepo) (*UserClaims, bool) {
	if apiTokens == nil {
		return nil, false
	}
	hash := repository.HashToken(raw)
	tok, err := apiTokens.FindActiveByHash(ctx, hash)
	if err != nil || tok == nil {
		return nil, false
	}
	admin, err := apiTokens.IsInstanceAdmin(ctx, tok.UserID)
	if err != nil {
		return nil, false
	}

	// Fire-and-forget: we don't want the request path blocked on this write
	// and the token is already authenticated, so a failed touch is survivable.
	go func(id uuid.UUID) {
		bg, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = apiTokens.TouchLastUsed(bg, id)
	}(tok.ID)

	// Empty-scope token = inherit full user perms (classic PAT). Represent
	// that by leaving TokenScopes as nil so ScopeAllows returns true.
	var scopes []string
	if len(tok.Scopes) > 0 {
		scopes = tok.Scopes
	}
	expires := tok.CreatedAt
	if tok.ExpiresAt != nil {
		expires = *tok.ExpiresAt
	}
	return &UserClaims{
		JTI:             tok.ID,
		UserID:          tok.UserID,
		IsInstanceAdmin: admin,
		ExpiresAt:       expires,
		TokenScopes:     scopes,
		FromToken:       true,
	}, true
}

// ClaimsFromContext retrieves UserClaims set by RequireAuth. Returns nil for unauthenticated requests.
func ClaimsFromContext(ctx context.Context) *UserClaims {
	c, _ := ctx.Value(claimsKey).(*UserClaims)
	return c
}
