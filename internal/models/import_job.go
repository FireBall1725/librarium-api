// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

// ImportJobArgs is the River job payload — lives here to avoid import cycles
// between the service and workers packages.
type ImportJobArgs struct {
	ImportJobID uuid.UUID `json:"import_job_id"`
}

func (ImportJobArgs) Kind() string { return "import_job" }

type ImportJobStatus string

const (
	ImportJobPending    ImportJobStatus = "pending"
	ImportJobProcessing ImportJobStatus = "processing"
	ImportJobDone       ImportJobStatus = "done"
	ImportJobFailed     ImportJobStatus = "failed"
	ImportJobCancelled  ImportJobStatus = "cancelled"
)

type ImportItemStatus string

const (
	ImportItemPending  ImportItemStatus = "pending"
	ImportItemDone     ImportItemStatus = "done"
	ImportItemSkipped  ImportItemStatus = "skipped"
	ImportItemFailed   ImportItemStatus = "failed"
)

// ImportOptions holds per-import configuration stored in the DB. The
// duplicate_* flags replace the older skip_duplicates toggle: with both
// off (the default) a duplicate ISBN is left untouched; turn either on
// to opt into bumping the copy count or refreshing the user-interaction
// fields from the CSV row. They compose, so both can be on at once.
//
// EnrichMetadata is fill-in-the-blanks only — the post-import enrichment
// run never overwrites a populated CSV field, so there is no per-field
// "prefer CSV" knob to keep here.
//
// AttributeToUserID redirects the user-interaction fields (read status,
// rating, review, dates, favourite) to a specific library member. When
// nil, the worker falls back to ImportJob.CreatedBy so a self-import
// behaves the same as before. Only instance admins may set a value
// other than the caller — the handler enforces that.
type ImportOptions struct {
	DuplicateIncrementCopyCount bool       `json:"duplicate_increment_count"`
	DuplicateUpdateFromCSV      bool       `json:"duplicate_update_from_csv"`
	DefaultFormat               string     `json:"default_format"`
	EnrichMetadata              bool       `json:"enrich_metadata"`
	EnrichCovers                bool       `json:"enrich_covers"`
	AttributeToUserID           *uuid.UUID `json:"attribute_to_user_id,omitempty"`
}

// MetadataEnrichmentJobArgs is the River job payload for async metadata enrichment.
type MetadataEnrichmentJobArgs struct {
	BookID    uuid.UUID `json:"book_id"`
	LibraryID uuid.UUID `json:"library_id"`
	CallerID  uuid.UUID `json:"caller_id"`
	// Force overwrites existing non-empty fields. When false only empty fields are filled.
	Force bool `json:"force"`
	// CoverOnly skips all text-field updates and only refreshes the book cover.
	CoverOnly bool `json:"cover_only,omitempty"`
}

func (MetadataEnrichmentJobArgs) Kind() string { return "metadata_enrichment" }

type ImportJob struct {
	ID            uuid.UUID       `json:"id"`
	LibraryID     uuid.UUID       `json:"library_id"`
	LibraryName   string          `json:"library_name,omitempty"`
	CreatedBy     uuid.UUID       `json:"created_by"`
	Status        ImportJobStatus `json:"status"`
	TotalRows     int             `json:"total_rows"`
	ProcessedRows int             `json:"processed_rows"`
	FailedRows    int             `json:"failed_rows"`
	SkippedRows   int             `json:"skipped_rows"`
	Options       ImportOptions   `json:"options"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	Items         []ImportJobItem `json:"items,omitempty"`
}

type ImportJobItem struct {
	ID          uuid.UUID        `json:"id"`
	ImportJobID uuid.UUID        `json:"import_job_id"`
	RowNumber   int              `json:"row_number"`
	RawData     map[string]string `json:"raw_data"`
	Status      ImportItemStatus `json:"status"`
	Title       string           `json:"title"`
	ISBN        string           `json:"isbn"`
	Message     string           `json:"message"`
	BookID      *uuid.UUID       `json:"book_id,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}
