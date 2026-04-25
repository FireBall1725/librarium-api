// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"encoding/csv"
	"fmt"
	"strings"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// ImportService handles creation and status queries for CSV import jobs.
type ImportService struct {
	importJobs  *repository.ImportJobRepo
	riverClient *river.Client[pgx.Tx]
}

func NewImportService(importJobs *repository.ImportJobRepo, riverClient *river.Client[pgx.Tx]) *ImportService {
	return &ImportService{importJobs: importJobs, riverClient: riverClient}
}

// ImportRequest carries the parsed CSV and options sent by the frontend.
type ImportRequest struct {
	LibraryID                   uuid.UUID
	CallerID                    uuid.UUID
	CSVText                     string
	FieldMapping                map[string]int // field name → CSV column index
	DuplicateIncrementCopyCount bool
	DuplicateUpdateFromCSV      bool
	DefaultFormat               string
	EnrichMetadata              bool
	EnrichCovers                bool
	AttributeToUserID           *uuid.UUID // nil → attribute to caller
}

// StartImport parses the CSV, stores the job + items, and enqueues a River job.
func (s *ImportService) StartImport(ctx context.Context, req ImportRequest) (*models.ImportJob, error) {
	rows, err := parseCSV(req.CSVText)
	if err != nil {
		return nil, fmt.Errorf("parsing CSV: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no rows in CSV")
	}

	jobID := uuid.New()
	job := &models.ImportJob{
		ID:        jobID,
		LibraryID: req.LibraryID,
		CreatedBy: req.CallerID,
		Status:    models.ImportJobPending,
		TotalRows: len(rows),
		Options: models.ImportOptions{
			DuplicateIncrementCopyCount: req.DuplicateIncrementCopyCount,
			DuplicateUpdateFromCSV:      req.DuplicateUpdateFromCSV,
			DefaultFormat:               models.NormalizeEditionFormat(req.DefaultFormat),
			EnrichMetadata:              req.EnrichMetadata,
			EnrichCovers:                req.EnrichCovers,
			AttributeToUserID:           req.AttributeToUserID,
		},
	}

	var items []models.ImportJobItem
	for i, row := range rows {
		rawData := mapRow(row, req.FieldMapping)
		item := models.ImportJobItem{
			ID:          uuid.New(),
			ImportJobID: jobID,
			RowNumber:   i + 1,
			RawData:     rawData,
			Title:       strings.TrimSpace(rawData["title"]),
			ISBN:        firstNonEmpty(rawData["isbn_13"], rawData["isbn_10"]),
		}
		items = append(items, item)
	}

	if err := s.importJobs.CreateJob(ctx, job, items); err != nil {
		return nil, fmt.Errorf("storing import job: %w", err)
	}

	if _, err := s.riverClient.Insert(ctx, models.ImportJobArgs{ImportJobID: jobID}, nil); err != nil {
		return nil, fmt.Errorf("enqueuing import job: %w", err)
	}

	return s.importJobs.GetJob(ctx, jobID)
}

// GetImportStatus returns the current status and items for an import job.
func (s *ImportService) GetImportStatus(ctx context.Context, libraryID, jobID uuid.UUID) (*models.ImportJob, error) {
	return s.importJobs.GetJobByLibrary(ctx, libraryID, jobID)
}

// ListImports returns all import jobs for a library, newest first.
func (s *ImportService) ListImports(ctx context.Context, libraryID uuid.UUID) ([]models.ImportJob, error) {
	return s.importJobs.ListByLibrary(ctx, libraryID)
}

// ListAllImports returns all import jobs created by a user, newest first.
func (s *ImportService) ListAllImports(ctx context.Context, userID uuid.UUID) ([]models.ImportJob, error) {
	return s.importJobs.ListByUser(ctx, userID)
}

// CancelImport cancels a pending or processing import job.
func (s *ImportService) CancelImport(ctx context.Context, jobID, userID uuid.UUID) error {
	return s.importJobs.CancelJob(ctx, jobID, userID)
}

// DeleteImport deletes a finished (done/failed/cancelled) import job.
func (s *ImportService) DeleteImport(ctx context.Context, jobID, userID uuid.UUID) error {
	return s.importJobs.DeleteJob(ctx, jobID, userID)
}

// DeleteFinishedImports deletes all finished import jobs for the user.
func (s *ImportService) DeleteFinishedImports(ctx context.Context, userID uuid.UUID) error {
	return s.importJobs.DeleteFinishedJobs(ctx, userID)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// parseCSV parses a raw CSV string into rows of string slices, handling
// quoted fields that may contain embedded newlines.
func parseCSV(text string) ([][]string, error) {
	r := csv.NewReader(strings.NewReader(text))
	r.LazyQuotes = true
	r.TrimLeadingSpace = true

	// Read all records at once; csv.Reader handles multi-line quoted fields.
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}

	// Skip the header row — always present in a valid CSV.
	if len(records) > 1 {
		return records[1:], nil
	}
	return nil, nil
}

// mapRow converts a CSV row (indexed by column) into a field map using the
// provided column→field mapping.
func mapRow(row []string, mapping map[string]int) map[string]string {
	out := make(map[string]string, len(mapping))
	for field, col := range mapping {
		if col >= 0 && col < len(row) {
			out[field] = strings.TrimSpace(row[col])
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
