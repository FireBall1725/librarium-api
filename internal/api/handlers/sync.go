// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/api/responses"
	"github.com/fireball1725/librarium-api/internal/repository"
)

const (
	syncDefaultLimit  = 500
	syncMaxLimit      = 1000
	syncApplyMaxBatch = 1000
)

type SyncHandler struct {
	repo *repository.SyncRepo
}

func NewSyncHandler(repo *repository.SyncRepo) *SyncHandler {
	return &SyncHandler{repo: repo}
}

// GetChanges returns the caller's data that has changed since the given timestamp.
//
// Clients advance `since` to max(updated_at) of returned ops; when has_more is
// true, call again immediately to keep draining the queue.
//
// v1 scope is user_book_interactions only. Other synced tables (loans, shelves,
// memberships, etc.) follow one at a time.
//
// This carried a TODO saying swag hit a parser bug here that broke type
// resolution in unrelated handlers, auth.go among them. It did not. auth.go
// failed on its own because it names responses.UserResponse in a comment while
// importing no such package, and one unresolvable annotation fails the whole
// run — so every file looked broken and this one, being the file under edit at
// the time, got the blame. sync.go is in fact the only handler that imports the
// package for real.
//
// @Summary     Pull changes since a timestamp
// @Description Returns the caller's data changed since `since`, oldest first. Advance `since` to the newest updated_at you applied. When has_more is true the response hit `limit` and you should call again straight away.
// @Tags        sync
// @Produce     json
// @Security    BearerAuth
// @Param       since  query     string   true   "RFC3339 timestamp; returns changes strictly newer than this"
// @Param       limit  query     integer  false  "Ops per response, 1 to 1000 (default 500)"
// @Success     200    {object}  github_com_fireball1725_librarium-api_internal_api_responses.SyncChangesResponse
// @Failure     400    {object}  object{error=string}
// @Failure     401    {object}  object{error=string}
// @Router      /sync/changes [get]
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

	// An empty list, not null. A nil slice marshals to `null`, and "nothing has
	// changed since you last asked" is the ordinary answer here: every
	// caught-up client got a null where its decoder expected an array, so sync
	// failed at every launch of a client that was already up to date.
	if ops == nil {
		ops = []responses.SyncOp{}
	}

	respond.JSON(w, http.StatusOK, responses.SyncChangesResponse{
		Ops:        ops,
		ServerTime: serverTime,
		HasMore:    hasMore,
	})
}

// ApplyChanges accepts a batch of outbox ops from a client and applies
// them with per-field LWW. Each op gets a status in the response so the
// client can clear its outbox accordingly.
//
// Request body shape (responses.SyncApplyRequest): { client_id?, ops: [...] }.
// Op shape (responses.SyncApplyOp): { op_id, entity_type, entity_id, field?, value?, deleted?, updated_at }.
//
// Response shape (responses.SyncApplyResponse): { results: [...], server_time }.
// Each result is { op_id, status, error? } where status is one of:
// applied / discarded_stale / not_found / invalid.
//
// v1 scope is user_book_interactions only and update-only (no creates
// through this endpoint; clients still POST/PUT new rows via the
// existing per-resource endpoints).
//
// @Summary     Push a batch of client changes
// @Description Applies client ops with per-field last-writer-wins. Every op comes back with its own status, so a partial success is normal and the client clears only the ops it sees applied. Update-only in v1: create new rows through the per-resource endpoints.
// @Tags        sync
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body      github_com_fireball1725_librarium-api_internal_api_responses.SyncApplyRequest  true  "Ops to apply, at most 1000"
// @Success     200   {object}  github_com_fireball1725_librarium-api_internal_api_responses.SyncApplyResponse
// @Failure     400   {object}  object{error=string}
// @Failure     401   {object}  object{error=string}
// @Router      /sync/apply [post]
func (h *SyncHandler) ApplyChanges(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	var req responses.SyncApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Ops) == 0 {
		respond.JSON(w, http.StatusOK, responses.SyncApplyResponse{
			Results:    []responses.SyncApplyResult{},
			ServerTime: time.Now().UTC(),
		})
		return
	}
	if len(req.Ops) > syncApplyMaxBatch {
		respond.Error(w, http.StatusBadRequest, "too many ops in one request (max 1000)")
		return
	}

	results := make([]responses.SyncApplyResult, 0, len(req.Ops))
	for _, op := range req.Ops {
		status, err := h.repo.ApplyUserBookInteractionOp(r.Context(), claims.UserID, op)
		if err != nil {
			results = append(results, responses.SyncApplyResult{
				OpID:   op.OpID,
				Status: "error",
				Error:  err.Error(),
			})
			continue
		}
		results = append(results, responses.SyncApplyResult{
			OpID:   op.OpID,
			Status: status,
		})
	}

	respond.JSON(w, http.StatusOK, responses.SyncApplyResponse{
		Results:    results,
		ServerTime: time.Now().UTC(),
	})
}
