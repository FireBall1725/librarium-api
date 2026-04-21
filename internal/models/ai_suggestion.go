// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

// AISuggestionRun is one execution of the suggestions pipeline for one user.
// Runs are persisted even on failure so the admin can audit cost and errors.
type AISuggestionRun struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	TriggeredBy      string // scheduler | admin | user
	ProviderType     string
	ModelID          string
	Status           string // running | completed | failed
	Error            string
	TokensIn         int
	TokensOut        int
	EstimatedCostUSD float64
	StartedAt        time.Time
	FinishedAt       *time.Time
}

// AISuggestion is one rendered recommendation — either a book to buy (not in
// library) or a book to read next (already owned but unread).
//
// RunID is a back-pointer to the pipeline execution that produced this row.
// It's nullable because the FK cascades to SET NULL — when an admin clears
// finished runs from the log we want the user's saved picks to survive, even
// though we lose the link to the run that generated them.
type AISuggestion struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	RunID         *uuid.UUID
	Type          string // buy | read_next
	BookID        *uuid.UUID
	BookEditionID *uuid.UUID
	Title         string
	Author        string
	ISBN          string
	CoverURL      string
	Reasoning     string
	Status        string // new | dismissed | interested | added_to_library
	CreatedAt     time.Time
}

// AISuggestionWithLibrary extends AISuggestion with the library_id resolved
// via a join on books. Used by the list endpoint so the UI can deep-link
// read_next suggestions without a follow-up lookup. LibraryID is nil for
// 'buy'-type rows (book_id is null, so the LEFT JOIN returns no match).
type AISuggestionWithLibrary struct {
	AISuggestion
	LibraryID *uuid.UUID
}

// AIBlockedItem is a "never suggest this again" block. Scope determines which
// fields are meaningful; see the CHECK constraint in the migration.
type AIBlockedItem struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Scope      string // book | author | series
	Title      string
	Author     string
	ISBN       string
	SeriesID   *uuid.UUID
	SeriesName string
	BlockedAt  time.Time
}

// AIRunEvent is a single observable step within a suggestions run — the
// pipeline emits these as it builds prompts, calls the AI, enriches candidates,
// and resolves read_next matches. Content is raw JSON so new event kinds
// don't require code changes on the read path.
type AIRunEvent struct {
	ID        uuid.UUID
	RunID     uuid.UUID
	Seq       int
	Type      string
	Content   []byte // JSONB
	CreatedAt time.Time
}

// AISuggestionsJobArgs is the River job payload for a per-user suggestions run.
// TriggeredBy distinguishes scheduler/admin/user for cost-attribution display.
type AISuggestionsJobArgs struct {
	UserID      uuid.UUID `json:"user_id"`
	TriggeredBy string    `json:"triggered_by"`
}

func (AISuggestionsJobArgs) Kind() string { return "ai_suggestions" }
