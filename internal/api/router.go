// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/fireball1725/librarium-api/internal/ai"
	"github.com/fireball1725/librarium-api/internal/api/handlers"
	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/auth"
	"github.com/fireball1725/librarium-api/internal/background"
	"github.com/fireball1725/librarium-api/internal/config"
	"github.com/fireball1725/librarium-api/internal/providers"
	bookProviders "github.com/fireball1725/librarium-api/internal/providers/books"
	mangaProviders "github.com/fireball1725/librarium-api/internal/providers/manga"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// MetricsCollector is the interface the router uses to record HTTP request metrics.
// Implemented by tui.Collector; nil means no metrics collection.
type MetricsCollector interface {
	RecordRequest(method, path, remoteAddr, client, errMsg string, status int, duration time.Duration)
}

func NewRouter(ctx context.Context, db *pgxpool.Pool, cfg *config.Config, riverClient *river.Client[pgx.Tx], metrics MetricsCollector) http.Handler {
	jwtSvc := auth.NewJWTService(cfg.JWTSecret, cfg.JWTAccessTTL)

	userRepo := repository.NewUserRepo(db)
	identityRepo := repository.NewIdentityRepo(db)
	tokenRepo := repository.NewTokenRepo(db)
	denylistRepo := repository.NewDenylistRepo(db)
	libraryRepo := repository.NewLibraryRepo(db)
	membershipRepo := repository.NewMembershipRepo(db)
	roleRepo := repository.NewRoleRepo(db)
	bookRepo := repository.NewBookRepo(db)
	contributorRepo := repository.NewContributorRepo(db)
	editionRepo := repository.NewEditionRepo(db)
	tagRepo := repository.NewTagRepo(db)
	shelfRepo := repository.NewShelfRepo(db)
	loanRepo := repository.NewLoanRepo(db)
	seriesRepo := repository.NewSeriesRepo(db)
	coverRepo := repository.NewCoverRepo(db)
	storageLocationRepo := repository.NewStorageLocationRepo(db)
	editionFileRepo := repository.NewEditionFileRepo(db)

	settingsRepo := repository.NewSettingsRepo(db)
	preferencesRepo := repository.NewPreferencesRepo(db)
	genreRepo := repository.NewGenreRepo(db)
	mediaTypeRepo := repository.NewMediaTypeRepo(db)
	importJobRepo := repository.NewImportJobRepo(db)

	// Build provider registry
	registry := providers.NewRegistry()
	registry.Register(bookProviders.NewTestProvider())
	registry.Register(bookProviders.NewOpenLibraryProvider())
	registry.Register(bookProviders.NewGoogleBooksProvider())
	registry.Register(bookProviders.NewISBNdbProvider())
	registry.Register(bookProviders.NewHardcoverProvider())
	registry.Register(mangaProviders.NewMangaDexProvider())

	providerSvc := service.NewProviderService(registry, settingsRepo)
	if err := providerSvc.LoadAll(context.Background()); err != nil {
		log.Printf("warning: failed to load provider settings: %v", err)
	}

	// AI providers (Anthropic / OpenAI / Ollama). Exactly one is active at a
	// time; admin picks via the Connections page.
	aiRegistry := ai.NewRegistry()
	aiRegistry.Register(ai.NewAnthropicProvider())
	aiRegistry.Register(ai.NewOpenAIProvider())
	aiRegistry.Register(ai.NewOllamaProvider())
	aiSvc := service.NewAIService(aiRegistry, settingsRepo)
	if err := aiSvc.LoadAll(context.Background()); err != nil {
		log.Printf("warning: failed to load AI provider settings: %v", err)
	}
	aiUserSvc := service.NewAIUserService(repository.NewUserAISettingsRepo(db))
	jobSvc := service.NewJobService(settingsRepo)
	aiSuggestionsRepo := repository.NewAISuggestionsRepo(db)
	suggestionsSvc := service.NewSuggestionsService(
		aiSuggestionsRepo, aiRegistry, aiSvc, jobSvc, aiUserSvc, providerSvc,
	)
	_ = suggestionsSvc // direct-call path (not used by HTTP handlers — jobs go through River)

	seriesVolumesRepo := repository.NewSeriesVolumesRepo(db)

	contributorSvc := service.NewContributorService(contributorRepo, bookRepo, coverRepo, registry, cfg.CoverStoragePath)

	loanSvc := service.NewLoanService(loanRepo, tagRepo)
	seriesSvc := service.NewSeriesService(seriesRepo, seriesVolumesRepo, tagRepo)
	releaseSyncSvc := service.NewReleaseSyncService(seriesRepo, seriesVolumesRepo, providerSvc)

	authSvc := service.NewAuthService(db, userRepo, identityRepo, tokenRepo, denylistRepo, jwtSvc, service.AuthConfig{
		AccessTTL:           cfg.JWTAccessTTL,
		RefreshTTL:          cfg.JWTRefreshTTL,
		RegistrationEnabled: cfg.RegistrationEnabled,
	})
	libSvc := service.NewLibraryService(db, libraryRepo, membershipRepo, roleRepo, userRepo, shelfRepo)
	bookSvc := service.NewBookService(db, bookRepo, contributorRepo, editionRepo, tagRepo, genreRepo, coverRepo, cfg.CoverStoragePath)
	editionFileSvc := service.NewEditionFileService(bookRepo, editionRepo, editionFileRepo, storageLocationRepo, cfg.EbookStoragePath, cfg.AudiobookStoragePath, cfg.EbookPathTemplate, cfg.AudiobookPathTemplate)
	shelfSvc := service.NewShelfService(shelfRepo, tagRepo)
	importSvc := service.NewImportService(importJobRepo, riverClient)
	enrichmentBatchRepo := repository.NewEnrichmentBatchRepo(db)

	providerHandler := handlers.NewProviderHandler(providerSvc)
	aiHandler := handlers.NewAIHandler(aiSvc)
	aiUserHandler := handlers.NewAIUserHandler(aiUserSvc)
	jobsHandler := handlers.NewJobsHandler(jobSvc)
	aiSuggestionsHandler := handlers.NewAISuggestionsHandler(aiSuggestionsRepo, riverClient, jobSvc)

	authHandler := handlers.NewAuthHandler(authSvc, preferencesRepo)
	setupHandler := handlers.NewSetupHandler(authSvc, userRepo)
	adminHandler := handlers.NewAdminHandler(authSvc)
	libraryHandler := handlers.NewLibraryHandler(libSvc)
	bookHandler := handlers.NewBookHandler(bookSvc, bookRepo, riverClient, enrichmentBatchRepo, editionFileSvc)
	shelfHandler := handlers.NewShelfHandler(shelfSvc)
	loanHandler := handlers.NewLoanHandler(loanSvc)
	seriesHandler := handlers.NewSeriesHandler(seriesSvc, releaseSyncSvc)
	importHandler := handlers.NewImportHandler(importSvc)
	genreHandler := handlers.NewGenreHandler(genreRepo)
	mediaTypeHandler := handlers.NewMediaTypeHandler(mediaTypeRepo)
	enrichmentHandler := handlers.NewEnrichmentBatchHandler(enrichmentBatchRepo)
	editionFileHandler := handlers.NewEditionFileHandler(editionFileSvc, bookSvc)
	storageLocationHandler := handlers.NewStorageLocationHandler(editionFileSvc)
	contributorHandler := handlers.NewContributorHandler(contributorSvc)
	dashboardHandler := handlers.NewDashboardHandler(bookRepo)

	releaseChecker := background.NewReleaseChecker(releaseSyncSvc, 24*time.Hour)
	go releaseChecker.Start(ctx)

	requireAuth := middleware.RequireAuth(jwtSvc, denylistRepo)
	// requireAdmin chains auth validation then instance-admin check
	requireAdmin := func(h http.Handler) http.Handler {
		return requireAuth(middleware.RequireInstanceAdmin(h))
	}
	// requireLibraryPerm chains auth then library permission check
	requireLibraryPerm := func(perm string, h http.Handler) http.Handler {
		return requireAuth(middleware.RequireLibraryPermission(db, perm)(h))
	}

	mux := http.NewServeMux()

	// Docs — no auth required
	mux.HandleFunc("GET /api/openapi.json", handlers.ServeOpenAPISpec)
	mux.HandleFunc("GET /api/docs", handlers.ServeScalarUI)

	mux.HandleFunc("GET /health", handlers.Health)

	// Dashboard
	mux.Handle("GET /api/v1/dashboard/currently-reading", requireAuth(http.HandlerFunc(dashboardHandler.GetCurrentlyReading)))
	mux.Handle("GET /api/v1/dashboard/recently-added", requireAuth(http.HandlerFunc(dashboardHandler.GetRecentlyAdded)))
	mux.Handle("GET /api/v1/dashboard/recently-finished", requireAuth(http.HandlerFunc(dashboardHandler.GetRecentlyFinished)))
	mux.Handle("GET /api/v1/dashboard/continue-series", requireAuth(http.HandlerFunc(dashboardHandler.GetContinueSeries)))
	mux.Handle("GET /api/v1/dashboard/picks-of-the-day", requireAuth(http.HandlerFunc(dashboardHandler.GetPicksOfTheDay)))
	mux.Handle("GET /api/v1/dashboard/stats", requireAuth(http.HandlerFunc(dashboardHandler.GetStats)))

	// Setup — public, used by clients to bootstrap a fresh instance
	mux.HandleFunc("GET /api/v1/setup/status", setupHandler.Status)
	mux.HandleFunc("POST /api/v1/setup/admin", setupHandler.BootstrapAdmin)

	// Auth — public
	mux.HandleFunc("POST /api/v1/auth/register", authHandler.Register)
	mux.HandleFunc("POST /api/v1/auth/login", authHandler.Login)
	mux.HandleFunc("POST /api/v1/auth/refresh", authHandler.Refresh)

	// Auth — protected
	mux.Handle("POST /api/v1/auth/logout", requireAuth(http.HandlerFunc(authHandler.Logout)))
	mux.Handle("GET /api/v1/auth/me", requireAuth(http.HandlerFunc(authHandler.Me)))
	mux.Handle("PUT /api/v1/auth/me", requireAuth(http.HandlerFunc(authHandler.UpdateMe)))
	mux.Handle("PUT /api/v1/auth/me/password", requireAuth(http.HandlerFunc(authHandler.UpdatePassword)))
	mux.Handle("GET /api/v1/auth/me/preferences", requireAuth(http.HandlerFunc(authHandler.GetPreferences)))
	mux.Handle("PATCH /api/v1/auth/me/preferences", requireAuth(http.HandlerFunc(authHandler.PatchPreferences)))

	// Admin — instance config (read-only view of runtime settings)
	mux.Handle("GET /api/v1/admin/config", requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"cover_storage_path":      cfg.CoverStoragePath,
				"ebook_storage_path":      cfg.EbookStoragePath,
				"audiobook_storage_path":  cfg.AudiobookStoragePath,
				"ebook_path_template":     cfg.EbookPathTemplate,
				"audiobook_path_template": cfg.AudiobookPathTemplate,
				"registration_enabled":    cfg.RegistrationEnabled,
			},
		})
	})))

	// Admin — provider settings (instance admin only)
	mux.Handle("GET /api/v1/admin/providers", requireAdmin(http.HandlerFunc(providerHandler.ListProviders)))
	mux.Handle("PUT /api/v1/admin/providers/{name}", requireAdmin(http.HandlerFunc(providerHandler.ConfigureProvider)))
	mux.Handle("POST /api/v1/admin/providers/{name}/test", requireAdmin(http.HandlerFunc(providerHandler.TestProvider)))
	mux.Handle("GET /api/v1/admin/providers/order", requireAdmin(http.HandlerFunc(providerHandler.GetProviderOrder)))
	mux.Handle("PUT /api/v1/admin/providers/order", requireAdmin(http.HandlerFunc(providerHandler.SetProviderOrder)))

	// Admin — AI connections (instance admin only)
	// More specific paths must come before /{provider} routes so the mux picks the right handler.
	mux.Handle("GET /api/v1/admin/connections/ai/permissions", requireAdmin(http.HandlerFunc(aiHandler.GetPermissions)))
	mux.Handle("PUT /api/v1/admin/connections/ai/permissions", requireAdmin(http.HandlerFunc(aiHandler.SetPermissions)))
	mux.Handle("POST /api/v1/admin/connections/ai/active", requireAdmin(http.HandlerFunc(aiHandler.SetActiveProvider)))
	mux.Handle("POST /api/v1/admin/connections/ai/{provider}/test", requireAdmin(http.HandlerFunc(aiHandler.TestProvider)))
	mux.Handle("PUT /api/v1/admin/connections/ai/{provider}", requireAdmin(http.HandlerFunc(aiHandler.ConfigureProvider)))
	mux.Handle("GET /api/v1/admin/connections/ai", requireAdmin(http.HandlerFunc(aiHandler.ListProviders)))

	// Admin — configurable scheduled jobs (instance admin only)
	mux.Handle("GET /api/v1/admin/jobs", requireAdmin(http.HandlerFunc(jobsHandler.ListJobs)))
	mux.Handle("GET /api/v1/admin/jobs/ai-suggestions", requireAdmin(http.HandlerFunc(jobsHandler.GetAISuggestionsJob)))
	mux.Handle("PUT /api/v1/admin/jobs/ai-suggestions", requireAdmin(http.HandlerFunc(jobsHandler.UpdateAISuggestionsJob)))
	mux.Handle("POST /api/v1/admin/jobs/ai-suggestions/run", requireAdmin(http.HandlerFunc(aiSuggestionsHandler.AdminRunSuggestions)))

	// User-scoped AI endpoints
	mux.Handle("GET /api/v1/me/ai-prefs", requireAuth(http.HandlerFunc(aiUserHandler.GetPrefs)))
	mux.Handle("PUT /api/v1/me/ai-prefs", requireAuth(http.HandlerFunc(aiUserHandler.UpdatePrefs)))
	mux.Handle("GET /api/v1/me/taste-profile", requireAuth(http.HandlerFunc(aiUserHandler.GetTasteProfile)))
	mux.Handle("PUT /api/v1/me/taste-profile", requireAuth(http.HandlerFunc(aiUserHandler.UpdateTasteProfile)))

	// User-scoped AI suggestions
	mux.Handle("GET /api/v1/me/suggestions", requireAuth(http.HandlerFunc(aiSuggestionsHandler.ListSuggestions)))
	mux.Handle("POST /api/v1/me/suggestions/run", requireAuth(http.HandlerFunc(aiSuggestionsHandler.RunNow)))
	mux.Handle("PUT /api/v1/me/suggestions/{id}/status", requireAuth(http.HandlerFunc(aiSuggestionsHandler.UpdateSuggestionStatus)))
	mux.Handle("POST /api/v1/me/suggestions/{id}/block", requireAuth(http.HandlerFunc(aiSuggestionsHandler.BlockSuggestion)))

	// Lookup (any authenticated user)
	mux.Handle("GET /api/v1/lookup/isbn/{isbn}", requireAuth(http.HandlerFunc(providerHandler.LookupISBN)))
	mux.Handle("GET /api/v1/lookup/isbn/{isbn}/merged", requireAuth(http.HandlerFunc(providerHandler.LookupISBNMerged)))
	mux.Handle("GET /api/v1/lookup/books", requireAuth(http.HandlerFunc(providerHandler.SearchBooks)))
	mux.Handle("GET /api/v1/lookup/series", requireAuth(http.HandlerFunc(providerHandler.SearchSeries)))
	mux.Handle("GET /api/v1/lookup/contributors", requireAuth(http.HandlerFunc(contributorHandler.SearchExternalContributors)))

	// Admin — instance admin only
	mux.Handle("GET /api/v1/admin/users", requireAdmin(http.HandlerFunc(adminHandler.ListUsers)))
	mux.Handle("POST /api/v1/admin/users", requireAdmin(http.HandlerFunc(adminHandler.CreateUser)))
	mux.Handle("PATCH /api/v1/admin/users/{id}", requireAdmin(http.HandlerFunc(adminHandler.UpdateUser)))
	mux.Handle("DELETE /api/v1/admin/users/{id}", requireAdmin(http.HandlerFunc(adminHandler.DeleteUser)))

	// Users — search (any authenticated user)
	mux.Handle("GET /api/v1/users", requireAuth(http.HandlerFunc(authHandler.SearchUsers)))

	// Media types (read: any authenticated user; write: instance admin)
	mux.Handle("GET /api/v1/media-types", requireAuth(http.HandlerFunc(mediaTypeHandler.ListMediaTypes)))
	mux.Handle("POST /api/v1/media-types", requireAdmin(http.HandlerFunc(mediaTypeHandler.CreateMediaType)))
	mux.Handle("PUT /api/v1/media-types/{media_type_id}", requireAdmin(http.HandlerFunc(mediaTypeHandler.UpdateMediaType)))
	mux.Handle("DELETE /api/v1/media-types/{media_type_id}", requireAdmin(http.HandlerFunc(mediaTypeHandler.DeleteMediaType)))

	// Contributors (any authenticated user) — search/create
	mux.Handle("GET /api/v1/contributors", requireAuth(http.HandlerFunc(bookHandler.SearchContributors)))
	mux.Handle("POST /api/v1/contributors", requireAuth(http.HandlerFunc(bookHandler.CreateContributor)))

	// Contributor profile, metadata, works (instance-scoped, auth required)
	mux.Handle("PATCH /api/v1/contributors/{contributor_id}", requireAuth(http.HandlerFunc(contributorHandler.UpdateContributor)))
	mux.Handle("DELETE /api/v1/contributors/{contributor_id}", requireAuth(http.HandlerFunc(contributorHandler.DeleteContributor)))
	mux.Handle("GET /api/v1/contributors/{contributor_id}/photo", requireAuth(http.HandlerFunc(contributorHandler.ServeContributorPhoto)))
	mux.Handle("PUT /api/v1/contributors/{contributor_id}/photo", requireAuth(http.HandlerFunc(contributorHandler.UploadContributorPhoto)))
	mux.Handle("DELETE /api/v1/contributors/{contributor_id}/photo", requireAuth(http.HandlerFunc(contributorHandler.DeleteContributorPhotoHandler)))
	mux.Handle("GET /api/v1/contributors/{contributor_id}/metadata/fetch", requireAuth(http.HandlerFunc(contributorHandler.FetchContributorMetadata)))
	mux.Handle("POST /api/v1/contributors/{contributor_id}/metadata/apply", requireAuth(http.HandlerFunc(contributorHandler.ApplyContributorMetadata)))
	mux.Handle("DELETE /api/v1/contributors/{contributor_id}/works/{work_id}", requireAuth(http.HandlerFunc(contributorHandler.DeleteContributorWork)))

	// Contributors within a library
	mux.Handle("GET /api/v1/libraries/{library_id}/contributors/letters", requireLibraryPerm("books:read", http.HandlerFunc(contributorHandler.GetLetters)))
	mux.Handle("GET /api/v1/libraries/{library_id}/contributors", requireLibraryPerm("books:read", http.HandlerFunc(contributorHandler.ListForLibrary)))
	mux.Handle("GET /api/v1/libraries/{library_id}/contributors/{contributor_id}", requireLibraryPerm("books:read", http.HandlerFunc(contributorHandler.GetContributor)))

	// Books
	mux.Handle("GET /api/v1/libraries/{library_id}/book-by-isbn/{isbn}", requireLibraryPerm("books:read", http.HandlerFunc(bookHandler.FindByISBN)))
	mux.Handle("GET /api/v1/libraries/{library_id}/books/letters", requireLibraryPerm("books:read", http.HandlerFunc(bookHandler.ListBookLetters)))
	mux.Handle("GET /api/v1/libraries/{library_id}/books/fingerprint", requireLibraryPerm("books:read", http.HandlerFunc(bookHandler.GetBookFingerprint)))
	mux.Handle("POST /api/v1/libraries/{library_id}/books/bulk/enrich", requireLibraryPerm("books:update", http.HandlerFunc(bookHandler.BulkEnrich)))
	mux.Handle("POST /api/v1/libraries/{library_id}/books/bulk/cover", requireLibraryPerm("books:update", http.HandlerFunc(bookHandler.BulkRefreshCovers)))
	mux.Handle("GET /api/v1/libraries/{library_id}/books", requireLibraryPerm("books:read", http.HandlerFunc(bookHandler.ListBooks)))
	mux.Handle("POST /api/v1/libraries/{library_id}/books", requireLibraryPerm("books:create", http.HandlerFunc(bookHandler.CreateBook)))
	mux.Handle("GET /api/v1/libraries/{library_id}/books/{book_id}", requireLibraryPerm("books:read", http.HandlerFunc(bookHandler.GetBook)))
	mux.Handle("PUT /api/v1/libraries/{library_id}/books/{book_id}", requireLibraryPerm("books:update", http.HandlerFunc(bookHandler.UpdateBook)))
	mux.Handle("DELETE /api/v1/libraries/{library_id}/books/{book_id}", requireLibraryPerm("books:delete", http.HandlerFunc(bookHandler.DeleteBook)))

	// Covers — all operations require library read permission
	mux.Handle("GET /api/v1/libraries/{library_id}/books/{book_id}/cover", requireLibraryPerm("books:read", http.HandlerFunc(bookHandler.ServeBookCover)))
	mux.Handle("POST /api/v1/libraries/{library_id}/books/{book_id}/cover/fetch", requireLibraryPerm("books:update", http.HandlerFunc(bookHandler.FetchBookCover)))
	mux.Handle("PUT /api/v1/libraries/{library_id}/books/{book_id}/cover", requireLibraryPerm("books:update", http.HandlerFunc(bookHandler.UploadBookCover)))
	mux.Handle("DELETE /api/v1/libraries/{library_id}/books/{book_id}/cover", requireLibraryPerm("books:update", http.HandlerFunc(bookHandler.DeleteBookCover)))

	// Editions
	mux.Handle("GET /api/v1/libraries/{library_id}/books/{book_id}/editions", requireLibraryPerm("books:read", http.HandlerFunc(bookHandler.ListEditions)))
	mux.Handle("POST /api/v1/libraries/{library_id}/books/{book_id}/editions", requireLibraryPerm("books:update", http.HandlerFunc(bookHandler.CreateEdition)))
	mux.Handle("PUT /api/v1/libraries/{library_id}/books/{book_id}/editions/{edition_id}", requireLibraryPerm("books:update", http.HandlerFunc(bookHandler.UpdateEdition)))
	mux.Handle("DELETE /api/v1/libraries/{library_id}/books/{book_id}/editions/{edition_id}", requireLibraryPerm("books:update", http.HandlerFunc(bookHandler.DeleteEdition)))

	// Edition files (multi-file)
	mux.Handle("POST /api/v1/libraries/{library_id}/books/{book_id}/editions/{edition_id}/files", requireLibraryPerm("books:update", http.HandlerFunc(editionFileHandler.UploadEditionFile)))
	mux.Handle("POST /api/v1/libraries/{library_id}/books/{book_id}/editions/{edition_id}/files/link", requireLibraryPerm("books:update", http.HandlerFunc(editionFileHandler.LinkEditionFile)))
	mux.Handle("POST /api/v1/libraries/{library_id}/books/{book_id}/editions/{edition_id}/files/link-upload", requireLibraryPerm("books:update", http.HandlerFunc(editionFileHandler.LinkUploadedFile)))
	mux.Handle("GET /api/v1/libraries/{library_id}/books/{book_id}/editions/{edition_id}/files/{file_id}", requireLibraryPerm("books:read", http.HandlerFunc(editionFileHandler.ServeEditionFile)))
	mux.Handle("DELETE /api/v1/libraries/{library_id}/books/{book_id}/editions/{edition_id}/files/{file_id}", requireLibraryPerm("books:update", http.HandlerFunc(editionFileHandler.DeleteEditionFile)))

	// Storage locations
	mux.Handle("GET /api/v1/libraries/{library_id}/storage-locations", requireLibraryPerm("books:read", http.HandlerFunc(storageLocationHandler.List)))
	mux.Handle("POST /api/v1/libraries/{library_id}/storage-locations", requireLibraryPerm("books:update", http.HandlerFunc(storageLocationHandler.Create)))
	mux.Handle("PUT /api/v1/libraries/{library_id}/storage-locations/{location_id}", requireLibraryPerm("books:update", http.HandlerFunc(storageLocationHandler.Update)))
	mux.Handle("DELETE /api/v1/libraries/{library_id}/storage-locations/{location_id}", requireLibraryPerm("books:update", http.HandlerFunc(storageLocationHandler.Delete)))
	mux.Handle("POST /api/v1/libraries/{library_id}/storage-locations/{location_id}/scan", requireLibraryPerm("books:update", http.HandlerFunc(storageLocationHandler.Scan)))
	mux.Handle("GET /api/v1/libraries/{library_id}/storage-locations/{location_id}/browse", requireLibraryPerm("books:read", http.HandlerFunc(editionFileHandler.BrowseStorageLocation)))
	mux.Handle("GET /api/v1/libraries/{library_id}/browse-uploads", requireLibraryPerm("books:read", http.HandlerFunc(editionFileHandler.BrowseUploadPath)))

	// User reading interactions (per edition, per caller)
	mux.Handle("GET /api/v1/libraries/{library_id}/books/{book_id}/editions/{edition_id}/my-interaction", requireLibraryPerm("books:read", http.HandlerFunc(bookHandler.GetMyInteraction)))
	mux.Handle("PUT /api/v1/libraries/{library_id}/books/{book_id}/editions/{edition_id}/my-interaction", requireLibraryPerm("books:read", http.HandlerFunc(bookHandler.UpsertMyInteraction)))
	mux.Handle("DELETE /api/v1/libraries/{library_id}/books/{book_id}/editions/{edition_id}/my-interaction", requireLibraryPerm("books:read", http.HandlerFunc(bookHandler.DeleteMyInteraction)))

	// Loans
	mux.Handle("GET /api/v1/libraries/{library_id}/loans", requireLibraryPerm("loans:read", http.HandlerFunc(loanHandler.ListLoans)))
	mux.Handle("POST /api/v1/libraries/{library_id}/loans", requireLibraryPerm("loans:create", http.HandlerFunc(loanHandler.CreateLoan)))
	mux.Handle("PATCH /api/v1/libraries/{library_id}/loans/{loan_id}", requireLibraryPerm("loans:update", http.HandlerFunc(loanHandler.UpdateLoan)))
	mux.Handle("DELETE /api/v1/libraries/{library_id}/loans/{loan_id}", requireLibraryPerm("loans:delete", http.HandlerFunc(loanHandler.DeleteLoan)))

	// Series
	mux.Handle("GET /api/v1/libraries/{library_id}/series", requireLibraryPerm("series:read", http.HandlerFunc(seriesHandler.ListSeries)))
	mux.Handle("POST /api/v1/libraries/{library_id}/series", requireLibraryPerm("series:create", http.HandlerFunc(seriesHandler.CreateSeries)))
	mux.Handle("GET /api/v1/libraries/{library_id}/series/suggest", requireLibraryPerm("series:read", http.HandlerFunc(seriesHandler.SuggestSeries)))
	mux.Handle("POST /api/v1/libraries/{library_id}/series/bulk-create", requireLibraryPerm("series:create", http.HandlerFunc(seriesHandler.BulkCreateSeries)))
	mux.Handle("GET /api/v1/libraries/{library_id}/series/{series_id}", requireLibraryPerm("series:read", http.HandlerFunc(seriesHandler.GetSeries)))
	mux.Handle("PUT /api/v1/libraries/{library_id}/series/{series_id}", requireLibraryPerm("series:update", http.HandlerFunc(seriesHandler.UpdateSeries)))
	mux.Handle("DELETE /api/v1/libraries/{library_id}/series/{series_id}", requireLibraryPerm("series:delete", http.HandlerFunc(seriesHandler.DeleteSeries)))
	mux.Handle("GET /api/v1/libraries/{library_id}/series/{series_id}/books", requireLibraryPerm("series:read", http.HandlerFunc(seriesHandler.ListSeriesBooks)))
	mux.Handle("POST /api/v1/libraries/{library_id}/series/{series_id}/books", requireLibraryPerm("series:update", http.HandlerFunc(seriesHandler.UpsertSeriesBook)))
	mux.Handle("DELETE /api/v1/libraries/{library_id}/series/{series_id}/books/{book_id}", requireLibraryPerm("series:update", http.HandlerFunc(seriesHandler.RemoveSeriesBook)))
	mux.Handle("GET /api/v1/libraries/{library_id}/series/{series_id}/volumes", requireLibraryPerm("series:read", http.HandlerFunc(seriesHandler.ListSeriesVolumes)))
	mux.Handle("POST /api/v1/libraries/{library_id}/series/{series_id}/volumes/sync", requireLibraryPerm("series:update", http.HandlerFunc(seriesHandler.SyncSeriesVolumes)))
	mux.Handle("GET /api/v1/libraries/{library_id}/series/{series_id}/match-candidates", requireLibraryPerm("series:read", http.HandlerFunc(seriesHandler.MatchCandidates)))
	mux.Handle("POST /api/v1/libraries/{library_id}/series/{series_id}/match-apply", requireLibraryPerm("series:update", http.HandlerFunc(seriesHandler.ApplyMatches)))

	// Shelves for a book
	mux.Handle("GET /api/v1/libraries/{library_id}/books/{book_id}/shelves", requireLibraryPerm("shelves:read", http.HandlerFunc(shelfHandler.ListBookShelves)))

	// Series for a book
	mux.Handle("GET /api/v1/libraries/{library_id}/books/{book_id}/series", requireLibraryPerm("series:read", http.HandlerFunc(seriesHandler.GetBookSeries)))

	// Shelves
	mux.Handle("GET /api/v1/libraries/{library_id}/shelves", requireLibraryPerm("shelves:read", http.HandlerFunc(shelfHandler.ListShelves)))
	mux.Handle("POST /api/v1/libraries/{library_id}/shelves", requireLibraryPerm("shelves:create", http.HandlerFunc(shelfHandler.CreateShelf)))
	mux.Handle("PUT /api/v1/libraries/{library_id}/shelves/{shelf_id}", requireLibraryPerm("shelves:update", http.HandlerFunc(shelfHandler.UpdateShelf)))
	mux.Handle("DELETE /api/v1/libraries/{library_id}/shelves/{shelf_id}", requireLibraryPerm("shelves:delete", http.HandlerFunc(shelfHandler.DeleteShelf)))
	mux.Handle("GET /api/v1/libraries/{library_id}/shelves/{shelf_id}/books", requireLibraryPerm("shelves:read", http.HandlerFunc(shelfHandler.ListShelfBooks)))
	mux.Handle("POST /api/v1/libraries/{library_id}/shelves/{shelf_id}/books", requireLibraryPerm("shelves:update", http.HandlerFunc(shelfHandler.AddBookToShelf)))
	mux.Handle("DELETE /api/v1/libraries/{library_id}/shelves/{shelf_id}/books/{book_id}", requireLibraryPerm("shelves:update", http.HandlerFunc(shelfHandler.RemoveBookFromShelf)))

	// Tags
	mux.Handle("GET /api/v1/libraries/{library_id}/tags", requireLibraryPerm("tags:read", http.HandlerFunc(shelfHandler.ListTags)))
	mux.Handle("POST /api/v1/libraries/{library_id}/tags", requireLibraryPerm("tags:create", http.HandlerFunc(shelfHandler.CreateTag)))
	mux.Handle("PUT /api/v1/libraries/{library_id}/tags/{tag_id}", requireLibraryPerm("tags:update", http.HandlerFunc(shelfHandler.UpdateTag)))
	mux.Handle("DELETE /api/v1/libraries/{library_id}/tags/{tag_id}", requireLibraryPerm("tags:delete", http.HandlerFunc(shelfHandler.DeleteTag)))

	// Libraries — auth required; list & create don't need library permission
	mux.Handle("GET /api/v1/libraries", requireAuth(http.HandlerFunc(libraryHandler.ListLibraries)))
	mux.Handle("POST /api/v1/libraries", requireAuth(http.HandlerFunc(libraryHandler.CreateLibrary)))
	mux.Handle("GET /api/v1/libraries/{library_id}", requireLibraryPerm("library:read", http.HandlerFunc(libraryHandler.GetLibrary)))
	mux.Handle("PUT /api/v1/libraries/{library_id}", requireLibraryPerm("library:update", http.HandlerFunc(libraryHandler.UpdateLibrary)))
	mux.Handle("DELETE /api/v1/libraries/{library_id}", requireLibraryPerm("library:delete", http.HandlerFunc(libraryHandler.DeleteLibrary)))

	// Members
	mux.Handle("GET /api/v1/libraries/{library_id}/members", requireLibraryPerm("members:read", http.HandlerFunc(libraryHandler.ListMembers)))
	mux.Handle("POST /api/v1/libraries/{library_id}/members", requireLibraryPerm("members:create", http.HandlerFunc(libraryHandler.AddMember)))
	mux.Handle("PATCH /api/v1/libraries/{library_id}/members/{user_id}", requireLibraryPerm("members:update", http.HandlerFunc(libraryHandler.UpdateMemberRole)))
	mux.Handle("DELETE /api/v1/libraries/{library_id}/members/{user_id}", requireLibraryPerm("members:delete", http.HandlerFunc(libraryHandler.RemoveMember)))

	// Global imports (across all libraries, scoped to the calling user)
	mux.Handle("GET /api/v1/imports", requireAuth(http.HandlerFunc(importHandler.ListAllImports)))
	mux.Handle("DELETE /api/v1/imports", requireAuth(http.HandlerFunc(importHandler.DeleteFinishedImports)))
	mux.Handle("POST /api/v1/imports/{import_id}/cancel", requireAuth(http.HandlerFunc(importHandler.CancelImport)))
	mux.Handle("DELETE /api/v1/imports/{import_id}", requireAuth(http.HandlerFunc(importHandler.DeleteImport)))

	// Enrichment batches (across all libraries, scoped to the calling user)
	mux.Handle("GET /api/v1/enrichment-batches", requireAuth(http.HandlerFunc(enrichmentHandler.ListAll)))
	mux.Handle("DELETE /api/v1/enrichment-batches", requireAuth(http.HandlerFunc(enrichmentHandler.DeleteFinished)))
	mux.Handle("GET /api/v1/enrichment-batches/{batch_id}", requireAuth(http.HandlerFunc(enrichmentHandler.Get)))
	mux.Handle("POST /api/v1/enrichment-batches/{batch_id}/cancel", requireAuth(http.HandlerFunc(enrichmentHandler.Cancel)))
	mux.Handle("DELETE /api/v1/enrichment-batches/{batch_id}", requireAuth(http.HandlerFunc(enrichmentHandler.Delete)))

	// Imports
	mux.Handle("GET /api/v1/libraries/{library_id}/imports", requireLibraryPerm("books:read", http.HandlerFunc(importHandler.ListImports)))
	mux.Handle("POST /api/v1/libraries/{library_id}/imports", requireLibraryPerm("books:create", http.HandlerFunc(importHandler.CreateImport)))
	mux.Handle("GET /api/v1/libraries/{library_id}/imports/{import_id}", requireLibraryPerm("books:read", http.HandlerFunc(importHandler.GetImport)))

	// Genres (instance-level; read for all authenticated, write for admins)
	mux.Handle("GET /api/v1/genres", requireAuth(http.HandlerFunc(genreHandler.ListGenres)))
	mux.Handle("POST /api/v1/genres", requireAdmin(http.HandlerFunc(genreHandler.CreateGenre)))
	mux.Handle("PUT /api/v1/genres/{genre_id}", requireAdmin(http.HandlerFunc(genreHandler.UpdateGenre)))
	mux.Handle("DELETE /api/v1/genres/{genre_id}", requireAdmin(http.HandlerFunc(genreHandler.DeleteGenre)))

	return middleware.Logger(mux, metrics)
}
