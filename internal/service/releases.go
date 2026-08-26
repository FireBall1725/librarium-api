// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

// ReleaseSyncService syncs series volume data from external providers.
type ReleaseSyncService struct {
	seriesRepo  *repository.SeriesRepo
	volumesRepo *repository.SeriesVolumesRepo
	providers   *ProviderService
}

func NewReleaseSyncService(seriesRepo *repository.SeriesRepo, volumesRepo *repository.SeriesVolumesRepo, providers *ProviderService) *ReleaseSyncService {
	return &ReleaseSyncService{seriesRepo: seriesRepo, volumesRepo: volumesRepo, providers: providers}
}

// SyncSeries fetches volume data for a single series from its linked provider.
func (s *ReleaseSyncService) SyncSeries(ctx context.Context, seriesID uuid.UUID) error {
	series, err := s.seriesRepo.FindByID(ctx, seriesID, uuid.Nil)
	if err != nil {
		return fmt.Errorf("finding series: %w", err)
	}
	if series.ExternalID == "" {
		return fmt.Errorf("series has no external id linked")
	}
	source := series.ExternalSource
	// Legacy rows may have external_id but no external_source. Try the first
	// enabled provider that supports series_volumes as a best-effort fallback.
	if source == "" {
		for _, p := range s.providers.Registry().All() {
			if !p.Enabled() {
				continue
			}
			info := p.Info()
			for _, cap := range info.Capabilities {
				if cap == "series_volumes" {
					source = info.Name
					slog.Warn("sync series: external_source missing, falling back to provider", "provider", source, "series_id", seriesID)
					break
				}
			}
			if source != "" {
				break
			}
		}
	}
	if source == "" {
		return fmt.Errorf("series has no external source linked and no series_volumes provider is available")
	}
	if err := s.syncFromSource(ctx, source, series.ExternalID, seriesID); err != nil {
		return err
	}
	// Promote here too, not only on the nightly pass. Someone who presses Sync
	// volumes is asking to see what is missing now, and a run that wrote rows
	// nothing can display looks exactly like a run that did nothing.
	if _, err := s.seriesRepo.PromoteVolumes(ctx, seriesID); err != nil &&
		!errors.Is(err, repository.ErrNoSeedBook) {
		return fmt.Errorf("promoting volumes: %w", err)
	}
	return nil
}

// SyncSummary is what a run of SyncAll actually did, so the job it runs under
// can say something more useful than "finished".
//
// The checker has been syncing every 24 hours for months with no record of what
// it found, which is how 448 volumes accumulated with nobody noticing that none
// of them could surface.
type SyncSummary struct {
	Series   int
	Failed   int
	Promoted int
	Matched  int
	// NoSeedBook counts series holding no book, so there was nothing to take a
	// media type from. Not an error: it is what an empty series looks like.
	NoSeedBook int
}

// SyncAll syncs volume data for every series that has an external source
// linked, then turns the volumes nobody holds into books.
//
// The second half is what makes the first half visible. Syncing writes rows
// into series_volumes, and until a volume becomes a book there is nowhere for
// it to appear: the ownership facet counts books, the series page lists books,
// and the rail's "Missing volume" row has always been looking for a book
// recorded against your series that no library holds.
func (s *ReleaseSyncService) SyncAll(ctx context.Context) SyncSummary {
	var out SyncSummary
	seriesList, err := s.volumesRepo.ListSeriesWithExternalSource(ctx)
	if err != nil {
		slog.Error("release sync: listing series failed", "error", err)
		return out
	}
	for _, series := range seriesList {
		out.Series++
		if err := s.syncFromSource(ctx, series.ExternalSource, series.ExternalID, series.ID); err != nil {
			out.Failed++
			slog.Warn("release sync: failed to sync series", "series_id", series.ID, "error", err)
			// Promotion still runs. A provider being down does not make the
			// volumes already on record any less promotable, and skipping them
			// would mean an outage quietly costs a night of visible gaps.
		}
		res, err := s.seriesRepo.PromoteVolumes(ctx, series.ID)
		switch {
		case errors.Is(err, repository.ErrNoSeedBook):
			out.NoSeedBook++
		case err != nil:
			out.Failed++
			slog.Warn("release sync: failed to promote volumes", "series_id", series.ID, "error", err)
		default:
			out.Promoted += res.Promoted
			out.Matched += res.Matched
		}
	}
	return out
}

func (s *ReleaseSyncService) syncFromSource(ctx context.Context, source, externalID string, seriesID uuid.UUID) error {
	provider := s.providers.Registry().SeriesVolumesProvider(source)
	if provider == nil {
		return fmt.Errorf("no volumes provider for source %q", source)
	}
	volumes, err := provider.FetchSeriesVolumes(ctx, externalID)
	if err != nil {
		return fmt.Errorf("fetching volumes: %w", err)
	}
	return s.volumesRepo.Sync(ctx, seriesID, volumes)
}
