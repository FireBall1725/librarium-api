// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package workers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/riverqueue/river"
)

// EnrichmentBatchWorker processes a bulk metadata or cover-refresh batch.
// It iterates over all books in the batch sequentially, updating the progress
// counters after each book so the user can track progress in the Jobs page.
type EnrichmentBatchWorker struct {
	river.WorkerDefaults[models.EnrichmentBatchJobArgs]

	batches        *repository.EnrichmentBatchRepo
	metadataWorker *MetadataWorker
}

func NewEnrichmentBatchWorker(
	batches *repository.EnrichmentBatchRepo,
	metadataWorker *MetadataWorker,
) *EnrichmentBatchWorker {
	return &EnrichmentBatchWorker{
		batches:        batches,
		metadataWorker: metadataWorker,
	}
}

// Timeout disables River's default 60s per-job timeout — bulk enrichment of a
// library can take many minutes (each book hits multiple provider HTTP calls),
// and having the job killed mid-run caused double-incremented counters and
// overwritten item statuses when River re-enqueued the work from scratch.
func (w *EnrichmentBatchWorker) Timeout(*river.Job[models.EnrichmentBatchJobArgs]) time.Duration {
	return -1
}

func (w *EnrichmentBatchWorker) Work(ctx context.Context, job *river.Job[models.EnrichmentBatchJobArgs]) error {
	batchID := job.Args.BatchID

	batch, err := w.batches.Get(ctx, batchID)
	if err != nil {
		return fmt.Errorf("loading enrichment batch: %w", err)
	}

	if batch.Status == models.EnrichmentBatchCancelled {
		slog.Info("enrichment batch cancelled before start", "batch_id", batchID)
		return nil
	}

	if err := w.batches.UpdateStatus(ctx, batchID, models.EnrichmentBatchProcessing); err != nil {
		return fmt.Errorf("marking batch processing: %w", err)
	}

	// On resume/retry, realign counters with the items table — a prior crashed
	// attempt may have left processed_books inflated or out of sync.
	if err := w.batches.ResyncCounters(ctx, batchID); err != nil {
		slog.Warn("enrichment batch counter resync failed", "batch_id", batchID, "error", err)
	}

	coverOnly := batch.Type == models.EnrichmentBatchTypeCover
	slog.Info("enrichment batch started",
		"batch_id", batchID,
		"type", batch.Type,
		"total", batch.TotalBooks,
		"force", batch.Force,
	)

	anyFailed := false
	for _, bookID := range batch.BookIDs {
		// Respect context cancellation (server shutdown).
		select {
		case <-ctx.Done():
			_ = w.batches.UpdateStatus(ctx, batchID, models.EnrichmentBatchCancelled)
			return ctx.Err()
		default:
		}

		// Respect user-initiated cancellation (DB status check).
		if current, rerr := w.batches.Get(ctx, batchID); rerr == nil && current.Status == models.EnrichmentBatchCancelled {
			slog.Info("enrichment batch cancelled mid-processing", "batch_id", batchID)
			return nil
		}

		// Find the item record for this book so we can update its status.
		item, itemErr := w.batches.FindItemByBookID(ctx, batchID, bookID)

		// Idempotency: if this item is already in a terminal state (a prior
		// attempt processed it before crashing), skip it — don't reprocess and
		// don't tick the counter again. ResyncCounters above already made
		// processed_books reflect these completed items.
		if itemErr == nil && item != nil && item.Status != models.EnrichmentItemPending {
			continue
		}

		bookErr := w.metadataWorker.ProcessBook(ctx, bookID, batch.LibraryID, batch.CreatedBy, batch.Force, coverOnly)

		var itemStatus models.EnrichmentBatchItemStatus
		var itemMsg string
		failed, skipped := false, false
		if bookErr != nil {
			if errors.Is(bookErr, ErrNoUpdate) {
				skipped = true
				itemStatus = models.EnrichmentItemSkipped
			} else {
				failed = true
				anyFailed = true
				itemStatus = models.EnrichmentItemFailed
				itemMsg = bookErr.Error()
				slog.Warn("enrichment batch book failed",
					"batch_id", batchID, "book_id", bookID, "error", bookErr)
			}
		} else {
			itemStatus = models.EnrichmentItemDone
		}

		if itemErr == nil && item != nil {
			_ = w.batches.UpdateItemStatus(ctx, item.ID, itemStatus, itemMsg)
		}

		processed, failedCount, total, incErr := w.batches.IncrementProcessed(ctx, batchID, failed, skipped)
		if incErr != nil {
			slog.Warn("enrichment batch counter update failed", "batch_id", batchID, "error", incErr)
			continue
		}

		// Mark done when all books have been attempted.
		if processed >= total {
			finalStatus := models.EnrichmentBatchDone
			if failedCount == total {
				finalStatus = models.EnrichmentBatchFailed
			}
			if err := w.batches.UpdateStatus(ctx, batchID, finalStatus); err != nil {
				slog.Warn("enrichment batch final status update failed", "batch_id", batchID, "error", err)
			}
		}
	}

	slog.Info("enrichment batch finished",
		"batch_id", batchID,
		"any_failed", anyFailed,
	)
	return nil
}
