// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/api/responses"
	"github.com/fireball1725/librarium-api/internal/repository"
)

const (
	syncDefaultLimit = 500
	syncMaxLimit     = 1000
)

type SyncHandler struct {
	repo *repository.SyncRepo
}

func NewSyncHandler(repo *repository.SyncRepo) *SyncHandler {
	return &SyncHandler{repo: repo}
}

// GetChanges returns the caller's data that has changed since the given timestamp.
//
// Query params: since (RFC3339, required), limit (1..1000, default 500).
//
// Response shape (responses.SyncChangesResponse): { ops, server_time, has_more }.
// Clients advance `since` to max(updated_at) of returned ops; when has_more is
// true, call again immediately to keep draining the queue.
//
// v1 scope is user_book_interactions only. Other synced tables (loans, shelves,
// memberships, etc.) follow one at a time.
//
// TODO: add swag annotations. The current swag 1.16.4 hits a parser bug when
// this handler references responses.SyncChangesResponse, which corrupts type
// resolution for unrelated handlers (auth.go's UserResponse fails to resolve).
// Tracked separately; the endpoint is documented inline above for now.
func (h *SyncHandler) GetChanges(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	sinceRaw := r.URL.Query().Get("since")
	if sinceRaw == "" {
		respond.Error(w, http.StatusBadRequest, "since is required (RFC3339 timestamp)")
		return
	}
	since, err := time.Parse(time.RFC3339Nano, sinceRaw)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "since must be an RFC3339 timestamp")
		return
	}

	limit := syncDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			respond.Error(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		if n > syncMaxLimit {
			n = syncMaxLimit
		}
		limit = n
	}

	// Capture server_time before the query so the client can use it as a
	// lower bound for the next sync if no ops are returned.
	serverTime := time.Now().UTC()

	ops, err := h.repo.UserBookInteractionChanges(r.Context(), claims.UserID, since, limit)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	// has_more is best-effort: if the query returned exactly `limit` rows, the
	// next page may exist. The client advances `since` to the max updated_at
	// and calls again. With per-field LWW emitting multiple ops per row, the
	// row count is bounded but ops aren't, so this can occasionally over-report.
	// Safe: an extra round-trip never hurts correctness.
	hasMore := len(ops) >= limit

	respond.JSON(w, http.StatusOK, responses.SyncChangesResponse{
		Ops:        ops,
		ServerTime: serverTime,
		HasMore:    hasMore,
	})
}
