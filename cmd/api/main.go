// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

// @title           Librarium API
// @version         26.0.0-dev
// @description     Self-hosted library tracking API. All protected endpoints require a Bearer JWT in the Authorization header.
// @termsOfService  http://localhost/

// @contact.name   Librarium
// @contact.url    https://github.com/fireball1725/librarium

// @license.name  AGPL-3.0
// @license.url   https://www.gnu.org/licenses/agpl-3.0.html

// @host      localhost:8080
// @BasePath  /api/v1

// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 Enter: Bearer {token}

package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	// Embed the IANA timezone database so the distroless runtime image can
	// resolve names like "America/Toronto" without a filesystem zoneinfo.
	_ "time/tzdata"

	"github.com/fireball1725/librarium-api/internal/api"
	"github.com/fireball1725/librarium-api/internal/config"
	"github.com/fireball1725/librarium-api/internal/db"
	"github.com/fireball1725/librarium-api/internal/providers"
	bookProviders "github.com/fireball1725/librarium-api/internal/providers/books"
	mangaProviders "github.com/fireball1725/librarium-api/internal/providers/manga"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/fireball1725/librarium-api/internal/tui"
	"github.com/fireball1725/librarium-api/internal/version"
	"github.com/fireball1725/librarium-api/internal/workers"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func main() {
	cfg := config.Load()

	baseCtx, cancelBaseCtx := context.WithCancel(context.Background())
	defer cancelBaseCtx()

	// ── TUI or plain logger ───────────────────────────────────────────────────
	var collector *tui.Collector
	if cfg.TUI {
		collector = tui.NewCollector()
		tuiHandler := tui.NewSlogHandler(collector, cfg.LogLevel)
		slog.SetDefault(slog.New(tuiHandler))
	} else {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: cfg.LogLevel,
		})))
	}

	slog.Info("librarium-api", "version", version.Version)

	// ── Migrations ────────────────────────────────────────────────────────────
	slog.Info("running database migrations")
	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	// ── Database pool ─────────────────────────────────────────────────────────
	pool, err := db.Connect(baseCtx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	slog.Info("database connected")

	// ── River migrations ──────────────────────────────────────────────────────
	slog.Info("running river migrations")
	riverMigrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		slog.Error("river migrator creation failed", "error", err)
		os.Exit(1)
	}
	if _, err := riverMigrator.Migrate(baseCtx, rivermigrate.DirectionUp, &rivermigrate.MigrateOpts{}); err != nil {
		slog.Error("river migration failed", "error", err)
		os.Exit(1)
	}

	// ── One-time backfills after migrations ───────────────────────────────────
	// Contributor sort_name was added in migration 000003 with a default of ''.
	// Derive and populate it for existing rows so sort-by-author works immediately.
	if err := backfillContributorSortNames(baseCtx, repository.NewContributorRepo(pool)); err != nil {
		slog.Warn("contributor sort_name backfill failed", "error", err)
	}

	// ── River workers ─────────────────────────────────────────────────────────
	// Build a standalone provider service for the worker (same config, separate instance).
	settingsRepo := repository.NewSettingsRepo(pool)
	registry := providers.NewRegistry()
	registry.Register(bookProviders.NewTestProvider())
	registry.Register(bookProviders.NewOpenLibraryProvider())
	registry.Register(bookProviders.NewGoogleBooksProvider())
	registry.Register(bookProviders.NewISBNdbProvider())
	registry.Register(bookProviders.NewHardcoverProvider())
	registry.Register(mangaProviders.NewMangaDexProvider())
	providerSvc := service.NewProviderService(registry, settingsRepo)
	if err := providerSvc.LoadAll(baseCtx); err != nil {
		slog.Warn("failed to load provider settings", "error", err)
	}

	workerBookSvc := service.NewBookService(
		pool,
		repository.NewBookRepo(pool),
		repository.NewContributorRepo(pool),
		repository.NewEditionRepo(pool),
		repository.NewTagRepo(pool),
		repository.NewGenreRepo(pool),
		repository.NewCoverRepo(pool),
		cfg.CoverStoragePath,
	)
	importWorker := workers.NewImportWorker(
		pool,
		repository.NewImportJobRepo(pool),
		repository.NewBookRepo(pool),
		repository.NewContributorRepo(pool),
		repository.NewEditionRepo(pool),
		repository.NewTagRepo(pool),
		repository.NewGenreRepo(pool),
		repository.NewEnrichmentBatchRepo(pool),
		nil, // riverClient set below after construction
	)
	metadataWorker := workers.NewMetadataWorker(
		repository.NewBookRepo(pool),
		repository.NewContributorRepo(pool),
		repository.NewEditionRepo(pool),
		repository.NewGenreRepo(pool),
		providerSvc,
		workerBookSvc,
	)
	enrichmentBatchWorker := workers.NewEnrichmentBatchWorker(
		repository.NewEnrichmentBatchRepo(pool),
		metadataWorker,
	)

	riverWorkers := river.NewWorkers()
	river.AddWorker(riverWorkers, importWorker)
	river.AddWorker(riverWorkers, metadataWorker)
	river.AddWorker(riverWorkers, enrichmentBatchWorker)

	riverClient, err := river.NewClient[pgx.Tx](riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 5},
		},
		Workers: riverWorkers,
	})
	if err != nil {
		slog.Error("river client creation failed", "error", err)
		os.Exit(1)
	}
	importWorker.SetRiverClient(riverClient)

	if err := riverClient.Start(baseCtx); err != nil {
		slog.Error("river client start failed", "error", err)
		os.Exit(1)
	}
	slog.Info("river worker started")

	// ── HTTP server ───────────────────────────────────────────────────────────
	addr := cfg.Host + ":" + cfg.Port
	// Only pass the collector when TUI is active — passing a nil *Collector as
	// the MetricsCollector interface produces a non-nil interface value, which
	// bypasses the nil check in the logger middleware and causes a panic.
	var metrics api.MetricsCollector
	if collector != nil {
		metrics = collector
	}
	handler := api.NewRouter(baseCtx, pool, cfg, riverClient, metrics)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	if collector != nil {
		srv.ConnState = func(_ net.Conn, state http.ConnState) {
			switch state {
			case http.StateNew:
				collector.TrackConn(+1)
			case http.StateClosed, http.StateHijacked:
				collector.TrackConn(-1)
			}
		}
	}

	go func() {
		slog.Info("starting librarium-api", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// ── TUI or wait for signal ────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	if cfg.TUI && collector != nil {
		// Poll the import job queue every 2 seconds and push stats to the TUI.
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					collector.UpdateQueueStats(pollQueueStats(baseCtx, pool))
				case <-baseCtx.Done():
					return
				}
			}
		}()
		tui.Run(collector, quit)
	} else {
		<-quit
	}

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	slog.Info("shutting down")
	cancelBaseCtx()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()

	if err := riverClient.StopAndCancel(shutCtx); err != nil {
		slog.Warn("river stop error", "error", err)
	}
	if err := srv.Shutdown(shutCtx); err != nil {
		slog.Error("forced shutdown", "error", err)
	}
	slog.Info("stopped")
}

// pollQueueStats queries both import_jobs and enrichment_batches for a queue snapshot.
func pollQueueStats(ctx context.Context, pool *pgxpool.Pool) tui.QueueStats {
	var s tui.QueueStats

	// Count by status across both job tables.
	countRows, err := pool.Query(ctx, `
		SELECT status, COUNT(*) FROM import_jobs      GROUP BY status
		UNION ALL
		SELECT status, COUNT(*) FROM enrichment_batches GROUP BY status`)
	if err != nil {
		return s
	}
	defer countRows.Close()
	for countRows.Next() {
		var status string
		var n int
		if err := countRows.Scan(&status, &n); err != nil {
			continue
		}
		switch status {
		case "pending":
			s.Pending += n
		case "processing":
			s.Processing += n
		case "done":
			s.Done += n
		case "failed":
			s.Failed += n
		}
	}

	// Active jobs from both tables, mapped to a common shape for display.
	activeRows, err := pool.Query(ctx, `
		SELECT ij.id::text, l.name,
		       ij.status, ij.total_rows     AS total, ij.processed_rows     AS processed,
		       ij.failed_rows, ij.skipped_rows, ij.updated_at
		FROM   import_jobs ij
		JOIN   libraries l ON l.id = ij.library_id
		WHERE  ij.status IN ('pending', 'processing')
		UNION ALL
		SELECT eb.id::text, l.name,
		       eb.status, eb.total_books AS total, eb.processed_books AS processed,
		       eb.failed_books, 0 AS skipped_rows, eb.updated_at
		FROM   enrichment_batches eb
		JOIN   libraries l ON l.id = eb.library_id
		WHERE  eb.status IN ('pending', 'processing')
		ORDER  BY updated_at DESC
		LIMIT  5`)
	if err != nil {
		return s
	}
	defer activeRows.Close()
	for activeRows.Next() {
		var info tui.ActiveJobInfo
		var rawID string
		if err := activeRows.Scan(
			&rawID, &info.LibraryName,
			&info.Status, &info.TotalRows, &info.ProcessedRows,
			&info.FailedRows, &info.SkippedRows, &info.UpdatedAt,
		); err != nil {
			continue
		}
		if len(rawID) >= 8 {
			info.ID = rawID[:8]
		} else {
			info.ID = rawID
		}
		s.Active = append(s.Active, info)
	}
	return s
}

// backfillContributorSortNames derives and persists sort_name for contributors
// that don't have one. Safe to run on every startup — it's a no-op once every
// row is populated.
func backfillContributorSortNames(ctx context.Context, repo *repository.ContributorRepo) error {
	missing, err := repo.ListMissingSortName(ctx)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	slog.Info("backfilling contributor sort_name", "count", len(missing))
	for _, c := range missing {
		var sortName string
		if c.IsCorporate {
			sortName = c.Name
		} else {
			sortName = service.DeriveSortName(c.Name)
		}
		if err := repo.SetSortName(ctx, c.ID, sortName); err != nil {
			slog.Warn("backfill sort_name failed", "contributor_id", c.ID, "name", c.Name, "error", err)
		}
	}
	return nil
}
