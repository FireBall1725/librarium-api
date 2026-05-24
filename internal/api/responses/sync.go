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
