// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/auth"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

type contextKey string

const claimsKey contextKey = "user_claims"

// UserClaims holds the decoded JWT data attached to a request context.
type UserClaims struct {
	JTI             uuid.UUID
	UserID          uuid.UUID
	IsInstanceAdmin bool
	ExpiresAt       time.Time
}

// RequireAuth validates the Bearer JWT, checks the denylist, and attaches
// UserClaims to the request context.
func RequireAuth(jwtSvc *auth.JWTService, denylist *repository.DenylistRepo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				respond.Error(w, http.StatusUnauthorized, "missing or invalid authorization header")
				return
			}
			tokenStr := strings.TrimPrefix(header, "Bearer ")

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

// ClaimsFromContext retrieves UserClaims set by RequireAuth. Returns nil for unauthenticated requests.
func ClaimsFromContext(ctx context.Context) *UserClaims {
	c, _ := ctx.Value(claimsKey).(*UserClaims)
	return c
}
