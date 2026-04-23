// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

// EnrichmentBatchType distinguishes metadata-only from cover-only batches.
type EnrichmentBatchType string

const (
	EnrichmentBatchTypeMetadata EnrichmentBatchType = "metadata"
	EnrichmentBatchTypeCover    EnrichmentBatchType = "cover"
)

// EnrichmentBatchStatus mirrors the import job status values so the Jobs page
// can treat both kinds of background work uniformly.
type EnrichmentBatchStatus string

const (
	EnrichmentBatchPending    EnrichmentBatchStatus = "pending"
	EnrichmentBatchProcessing EnrichmentBatchStatus = "processing"
	EnrichmentBatchDone       EnrichmentBatchStatus = "done"
	EnrichmentBatchFailed     EnrichmentBatchStatus = "failed"
	EnrichmentBatchCancelled  EnrichmentBatchStatus = "cancelled"
)

// EnrichmentBatchItemStatus mirrors import item statuses.
type EnrichmentBatchItemStatus string

const (
	EnrichmentItemPending EnrichmentBatchItemStatus = "pending"
	EnrichmentItemDone    EnrichmentBatchItemStatus = "done"
	EnrichmentItemFailed  EnrichmentBatchItemStatus = "failed"
	EnrichmentItemSkipped EnrichmentBatchItemStatus = "skipped"
)

// EnrichmentBatchItem tracks the per-book result within an enrichment batch.
type EnrichmentBatchItem struct {
	ID        uuid.UUID                 `json:"id"`
	BatchID   uuid.UUID                 `json:"batch_id"`
	BookID    *uuid.UUID                `json:"book_id,omitempty"`
	BookTitle string                    `json:"book_title"`
	Status    EnrichmentBatchItemStatus `json:"status"`
	Message   string                    `json:"message,omitempty"`
	CreatedAt time.Time                 `json:"created_at"`
	UpdatedAt time.Time                 `json:"updated_at"`
}

// EnrichmentBatch is the application-level tracking record for a bulk
// metadata or cover refresh operation.
type EnrichmentBatch struct {
	ID             uuid.UUID              `json:"id"`
	// LibraryID scopes the batch to a library when set. Null for
	// floating-book batches (e.g. re-enriching a suggestion-backed book not
	// yet held by any library).
	LibraryID      *uuid.UUID             `json:"library_id,omitempty"`
	LibraryName    string                 `json:"library_name,omitempty"`
	CreatedBy      uuid.UUID              `json:"created_by"`
	Type           EnrichmentBatchType    `json:"type"`
	Force          bool                   `json:"force"`
	Status         EnrichmentBatchStatus  `json:"status"`
	BookIDs        []uuid.UUID            `json:"book_ids,omitempty"`
	TotalBooks     int                    `json:"total_books"`
	ProcessedBooks int                    `json:"processed_books"`
	FailedBooks    int                    `json:"failed_books"`
	SkippedBooks   int                    `json:"skipped_books"`
	Items          []EnrichmentBatchItem  `json:"items,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

// EnrichmentBatchJobArgs is the River job payload for a batch enrichment run.
type EnrichmentBatchJobArgs struct {
	BatchID uuid.UUID `json:"batch_id"`
}

func (EnrichmentBatchJobArgs) Kind() string { return "enrichment_batch" }
