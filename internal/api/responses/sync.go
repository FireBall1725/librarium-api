// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package responses

import (
	"time"

	"github.com/google/uuid"
)

// SyncChangesResponse is the wire shape for GET /sync/changes.
//
// Clients call /sync/changes?since=<ts> and apply each op locally.
// On the next call, they pass since=<max(updated_at) of previously
// applied ops>. When HasMore is true, the response was capped by the
// limit and the client should call again immediately to keep draining.
type SyncChangesResponse struct {
	Ops        []SyncOp  `json:"ops"`
	ServerTime time.Time `json:"server_time"`
	HasMore    bool      `json:"has_more"`
}

// SyncOp is a single change emitted by the sync delta endpoint.
//
// Per-field LWW fields emit one op per changed field (Field set, Value
// holds the new field value). Tombstones emit one op with Deleted=true
// and Value=null. Field is left empty for row-level / set-membership ops
// in tables that don't use per-field LWW.
type SyncOp struct {
	EntityType string      `json:"entity_type"`
	EntityID   uuid.UUID   `json:"entity_id"`
	Field      string      `json:"field,omitempty"`
	Value      interface{} `json:"value,omitempty"`
	Deleted    bool        `json:"deleted,omitempty"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// SyncApplyRequest is the wire shape for POST /sync/apply.
//
// The client batches outbox ops (per-field LWW changes and tombstones
// written while offline) and pushes them. The server reconciles each
// op via field-level last-write-wins and returns a per-op status. The
// endpoint is idempotent: replaying the same ops just produces
// discarded_stale results since the timestamps no longer beat what's
// in the database.
type SyncApplyRequest struct {
	ClientID uuid.UUID      `json:"client_id,omitempty"`
	Ops      []SyncApplyOp  `json:"ops"`
}

// SyncApplyOp mirrors SyncOp but is what the client sends back to the
// server. OpID is a client-generated UUID the client uses to correlate
// the per-op result.
type SyncApplyOp struct {
	OpID       uuid.UUID   `json:"op_id"`
	EntityType string      `json:"entity_type"`
	EntityID   uuid.UUID   `json:"entity_id"`
	Field      string      `json:"field,omitempty"`
	Value      interface{} `json:"value,omitempty"`
	Deleted    bool        `json:"deleted,omitempty"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// SyncApplyResponse is the wire shape returned by POST /sync/apply.
type SyncApplyResponse struct {
	Results    []SyncApplyResult `json:"results"`
	ServerTime time.Time         `json:"server_time"`
}

// SyncApplyResult is the per-op outcome.
//
// Status values:
//   - "applied"          field was updated (or row was soft-deleted)
//   - "discarded_stale"  server's timestamp for this field is newer; op is older
//   - "not_found"        no row matches entity_id (or caller doesn't own it)
//   - "invalid"          op shape was malformed (unknown field, bad value type, etc.)
type SyncApplyResult struct {
	OpID   uuid.UUID `json:"op_id"`
	Status string    `json:"status"`
	Error  string    `json:"error,omitempty"`
}
