// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
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
	return s.syncFromSource(ctx, source, series.ExternalID, seriesID)
}

// SyncAll syncs volume data for every series that has an external source linked.
// Intended for use by the background release checker.
func (s *ReleaseSyncService) SyncAll(ctx context.Context) {
	seriesList, err := s.volumesRepo.ListSeriesWithExternalSource(ctx)
	if err != nil {
		slog.Error("release sync: listing series failed", "error", err)
		return
	}
	for _, series := range seriesList {
		if err := s.syncFromSource(ctx, series.ExternalSource, series.ExternalID, series.ID); err != nil {
			slog.Warn("release sync: failed to sync series", "series_id", series.ID, "error", err)
		}
	}
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
