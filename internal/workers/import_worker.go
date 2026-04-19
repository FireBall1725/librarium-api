// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

// Package workers contains River job workers.
package workers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// importedBook holds the ID and title of a successfully created book for batch enrichment.
type importedBook struct {
	id    uuid.UUID
	title string
}

// ImportWorker processes one CSV import job from start to finish.
type ImportWorker struct {
	river.WorkerDefaults[models.ImportJobArgs]

	pool         *pgxpool.Pool
	importJobs   *repository.ImportJobRepo
	books        *repository.BookRepo
	contributors *repository.ContributorRepo
	editions     *repository.EditionRepo
	tags         *repository.TagRepo
	genres       *repository.GenreRepo
	batches      *repository.EnrichmentBatchRepo
	riverClient  *river.Client[pgx.Tx]
}

func NewImportWorker(
	pool *pgxpool.Pool,
	importJobs *repository.ImportJobRepo,
	books *repository.BookRepo,
	contributors *repository.ContributorRepo,
	editions *repository.EditionRepo,
	tags *repository.TagRepo,
	genres *repository.GenreRepo,
	batches *repository.EnrichmentBatchRepo,
	riverClient *river.Client[pgx.Tx],
) *ImportWorker {
	return &ImportWorker{
		pool:         pool,
		importJobs:   importJobs,
		books:        books,
		contributors: contributors,
		editions:     editions,
		tags:         tags,
		genres:       genres,
		batches:      batches,
		riverClient:  riverClient,
	}
}

// SetRiverClient wires in the River client after it has been constructed.
// Called from main after river.NewClient to break the initialization cycle.
func (w *ImportWorker) SetRiverClient(c *river.Client[pgx.Tx]) {
	w.riverClient = c
}

func (w *ImportWorker) Work(ctx context.Context, job *river.Job[models.ImportJobArgs]) error {
	jobID := job.Args.ImportJobID
	slog.Info("import job started", "import_job_id", jobID)

	// Load job first — bail early if it was cancelled before River picked it up.
	importJob, err := w.importJobs.GetJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("loading import job: %w", err)
	}
	if importJob.Status == models.ImportJobCancelled {
		slog.Info("import job was cancelled before processing", "import_job_id", jobID)
		return nil
	}

	if err := w.importJobs.UpdateJobStatus(ctx, jobID, models.ImportJobProcessing, 0, 0, 0); err != nil {
		return fmt.Errorf("marking job as processing: %w", err)
	}

	items, err := w.importJobs.ListPendingItems(ctx, jobID)
	if err != nil {
		return fmt.Errorf("loading pending items: %w", err)
	}

	tagCache := make(map[string]uuid.UUID) // lowercase name → id

	allGenres, err := w.genres.List(ctx)
	if err != nil {
		return fmt.Errorf("loading genres: %w", err)
	}

	var processed, failed, skipped int
	var newBooks []importedBook // tracks newly created books for post-import enrichment batches
	for _, item := range items {
		// Check for cancellation before each item so the worker stops promptly.
		if current, cerr := w.importJobs.GetJob(ctx, jobID); cerr == nil && current.Status == models.ImportJobCancelled {
			slog.Info("import job cancelled mid-processing", "import_job_id", jobID, "processed", processed)
			return nil
		}

		status, msg, bookID := w.processItem(ctx, importJob, &item, tagCache, allGenres)
		_ = w.importJobs.UpdateItemStatus(ctx, item.ID, status, msg, bookID)

		switch status {
		case models.ImportItemDone:
			processed++
			if bookID != nil && item.Title != "" {
				newBooks = append(newBooks, importedBook{id: *bookID, title: item.Title})
			}
		case models.ImportItemFailed:
			failed++
			slog.Warn("import row failed",
				"import_job_id", jobID,
				"row", item.RowNumber,
				"title", item.Title,
				"isbn", item.ISBN,
				"error", msg,
			)
		case models.ImportItemSkipped:
			skipped++
		}
		_ = w.importJobs.UpdateJobStatus(ctx, jobID, models.ImportJobProcessing, processed, failed, skipped)
	}

	finalStatus := models.ImportJobDone
	if err := w.importJobs.UpdateJobStatus(ctx, jobID, finalStatus, processed, failed, skipped); err != nil {
		return fmt.Errorf("finalizing import job: %w", err)
	}

	// After the import completes, spawn tracked enrichment batches so progress
	// appears in the Jobs page and River TUI as a single cohesive job.
	opts := importJob.Options
	if len(newBooks) > 0 && w.riverClient != nil && w.batches != nil {
		if opts.EnrichMetadata {
			w.spawnEnrichmentBatch(ctx, importJob, newBooks, models.EnrichmentBatchTypeMetadata)
		}
		if opts.EnrichCovers {
			w.spawnEnrichmentBatch(ctx, importJob, newBooks, models.EnrichmentBatchTypeCover)
		}
	}

	slog.Info("import job done",
		"import_job_id", jobID,
		"processed", processed,
		"failed", failed,
		"skipped", skipped,
	)
	return nil
}

// spawnEnrichmentBatch creates an EnrichmentBatch record + items in the database and
// enqueues a single EnrichmentBatchJobArgs River job.  This makes the post-import
// enrichment visible in the Jobs page and the River TUI.
func (w *ImportWorker) spawnEnrichmentBatch(
	ctx context.Context,
	importJob *models.ImportJob,
	books []importedBook,
	batchType models.EnrichmentBatchType,
) {
	batchID := uuid.New()
	bookIDs := make([]uuid.UUID, len(books))
	for i, b := range books {
		bookIDs[i] = b.id
	}

	batch := &models.EnrichmentBatch{
		ID:         batchID,
		LibraryID:  importJob.LibraryID,
		CreatedBy:  importJob.CreatedBy,
		Type:       batchType,
		Force:      false,
		Status:     models.EnrichmentBatchPending,
		BookIDs:    bookIDs,
		TotalBooks: len(books),
	}
	if err := w.batches.Create(ctx, batch); err != nil {
		slog.Warn("creating enrichment batch after import", "type", batchType, "error", err)
		return
	}

	items := make([]models.EnrichmentBatchItem, len(books))
	for i, b := range books {
		bookIDCopy := b.id
		items[i] = models.EnrichmentBatchItem{
			ID:        uuid.New(),
			BatchID:   batchID,
			BookID:    &bookIDCopy,
			BookTitle: b.title,
			Status:    models.EnrichmentItemPending,
		}
	}
	if err := w.batches.CreateItems(ctx, items); err != nil {
		slog.Warn("creating enrichment batch items after import", "type", batchType, "error", err)
		return
	}

	if _, err := w.riverClient.Insert(ctx, models.EnrichmentBatchJobArgs{BatchID: batchID}, nil); err != nil {
		slog.Warn("enqueuing enrichment batch job after import", "type", batchType, "error", err)
	}
}

func (w *ImportWorker) processItem(
	ctx context.Context,
	job *models.ImportJob,
	item *models.ImportJobItem,
	tagCache map[string]uuid.UUID,
	allGenres []*models.Genre,
) (models.ImportItemStatus, string, *uuid.UUID) {
	opts := job.Options
	row := item.RawData

	title := strings.TrimSpace(row["title"])
	if title == "" {
		return models.ImportItemSkipped, "no title", nil
	}

	isbn := strings.TrimSpace(row["isbn_13"])
	if isbn == "" {
		isbn = strings.TrimSpace(row["isbn_10"])
	}

	// ── Duplicate check (ISBN deduplication at edition level) ─────────────────
	if isbn != "" {
		existing, err := w.editions.FindByISBN(ctx, job.LibraryID, isbn)
		if err == nil && existing != nil {
			if incrErr := w.editions.IncrementCopyCount(ctx, existing.ID); incrErr != nil {
				return models.ImportItemFailed, fmt.Sprintf("increment copy count: %v", incrErr), nil
			}
			bookID := existing.BookID
			return models.ImportItemDone, fmt.Sprintf("duplicate ISBN %s — copy count incremented", isbn), &bookID
		}
	}

	// CSV values are used directly; provider enrichment happens asynchronously
	// via MetadataEnrichmentJob when opts.EnrichMetadata is true.
	finalTitle := title
	finalSubtitle := row["subtitle"]
	finalDescription := row["description"]
	finalPublisher := row["publisher"]
	finalLanguage := row["language"]
	finalISBN10 := strings.TrimSpace(row["isbn_10"])
	finalISBN13 := strings.TrimSpace(row["isbn_13"])

	var publishDate *time.Time
	if ds := strings.TrimSpace(row["publish_date"]); ds != "" {
		for _, layout := range []string{"2006-01-02", "2006-01", "2006", "January 2, 2006", "Jan 2, 2006"} {
			if t, err := time.Parse(layout, ds); err == nil {
				publishDate = &t
				break
			}
		}
	}

	// ── Media type ────────────────────────────────────────────────────────────
	mediaTypes, err := w.books.ListMediaTypes(ctx)
	if err != nil {
		return models.ImportItemFailed, fmt.Sprintf("loading media types: %v", err), nil
	}
	mediaTypeID := findMediaTypeID(mediaTypes, row["media_type"])
	if mediaTypeID == uuid.Nil {
		mediaTypeID = inferMediaType(mediaTypes, row["tags"])
	}
	if mediaTypeID == uuid.Nil {
		for _, mt := range mediaTypes {
			if mt.Name == "novel" {
				mediaTypeID = mt.ID
				break
			}
		}
	}

	// ── Contributors ──────────────────────────────────────────────────────────
	var contribs []repository.ContributorInput
	if authorStr := strings.TrimSpace(row["author"]); authorStr != "" {
		for i, rawName := range splitAuthors(authorStr) {
			name, role := parseContributorNameRole(rawName)
			c, err := w.findOrCreateContributor(ctx, name)
			if err != nil {
				slog.Warn("contributor find/create failed", "name", name, "error", err)
				continue
			}
			contribs = append(contribs, repository.ContributorInput{
				ContributorID: c.ID,
				Role:          role,
				DisplayOrder:  i,
			})
		}
	}

	// ── Tags ──────────────────────────────────────────────────────────────────
	var tagIDs []uuid.UUID
	if tagStr := row["tags"]; tagStr != "" {
		for _, rawName := range strings.Split(tagStr, ",") {
			name := strings.TrimSpace(rawName)
			if name == "" {
				continue
			}
			id, err := w.resolveTag(ctx, job.LibraryID, job.CreatedBy, name, tagCache)
			if err != nil {
				slog.Warn("resolving tag", "name", name, "error", err)
				continue
			}
			tagIDs = append(tagIDs, id)
		}
	}

	// ── Genres (from CSV tags only; provider enrichment adds more if enabled) ─
	var genreIDs []uuid.UUID
	if tagStr := row["tags"]; tagStr != "" {
		var csvTagParts []string
		for _, t := range strings.Split(tagStr, ",") {
			if p := strings.TrimSpace(t); p != "" {
				csvTagParts = append(csvTagParts, p)
			}
		}
		if len(csvTagParts) > 0 {
			genreIDs = normalizeCategories(csvTagParts, allGenres)
		}
	}

	// ── Page count ────────────────────────────────────────────────────────────
	var pageCount *int
	if pc := strings.TrimSpace(row["page_count"]); pc != "" {
		var n int
		if _, scanErr := fmt.Sscanf(pc, "%d", &n); scanErr == nil && n > 0 {
			pageCount = &n
		}
	}

	// ── Create book in transaction ────────────────────────────────────────────
	bookID := uuid.New()
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return models.ImportItemFailed, fmt.Sprintf("begin tx: %v", err), nil
	}
	defer tx.Rollback(ctx)

	if err := w.books.Create(ctx, tx, bookID, job.LibraryID,
		finalTitle, finalSubtitle, mediaTypeID,
		finalDescription, job.CreatedBy,
	); err != nil {
		return models.ImportItemFailed, fmt.Sprintf("creating book: %v", err), nil
	}

	if len(contribs) > 0 {
		if err := w.books.SetContributors(ctx, tx, bookID, contribs); err != nil {
			return models.ImportItemFailed, fmt.Sprintf("setting contributors: %v", err), nil
		}
	}

	if len(tagIDs) > 0 {
		if err := w.tags.SetBookTags(ctx, tx, bookID, tagIDs); err != nil {
			return models.ImportItemFailed, fmt.Sprintf("setting tags: %v", err), nil
		}
	}

	if len(genreIDs) > 0 {
		if err := w.genres.SetBookGenres(ctx, tx, bookID, genreIDs); err != nil {
			return models.ImportItemFailed, fmt.Sprintf("setting genres: %v", err), nil
		}
	}

	format := models.NormalizeEditionFormat(opts.DefaultFormat)
	editionLang := finalLanguage
	if editionLang == "" {
		editionLang = "en"
	}
	// ── Acquired date ─────────────────────────────────────────────────────────
	var acquiredAt *time.Time
	if ds := strings.TrimSpace(row["acquired_date"]); ds != "" {
		for _, layout := range []string{"2006-01-02", "2006-01", "2006", "January 2, 2006", "Jan 2, 2006"} {
			if t, err := time.Parse(layout, ds); err == nil {
				acquiredAt = &t
				break
			}
		}
	}

	if err := w.editions.Create(ctx, tx, uuid.New(), bookID,
		format, editionLang, "", "", finalPublisher,
		publishDate, finalISBN10, finalISBN13, finalDescription,
		nil, pageCount, true, acquiredAt, nil,
	); err != nil {
		return models.ImportItemFailed, fmt.Sprintf("creating edition: %v", err), nil
	}

	if err := tx.Commit(ctx); err != nil {
		return models.ImportItemFailed, fmt.Sprintf("commit: %v", err), nil
	}

	return models.ImportItemDone, fmt.Sprintf("imported %q", finalTitle), &bookID
}

func (w *ImportWorker) findOrCreateContributor(ctx context.Context, name string) (*models.Contributor, error) {
	results, err := w.contributors.Search(ctx, name, 5)
	if err != nil {
		return nil, err
	}
	for _, c := range results {
		if strings.EqualFold(c.Name, name) {
			return c, nil
		}
	}
	return w.contributors.Create(ctx, uuid.New(), name, service.DeriveSortName(name), false)
}

func (w *ImportWorker) resolveTag(ctx context.Context, libraryID, createdBy uuid.UUID, name string, cache map[string]uuid.UUID) (uuid.UUID, error) {
	key := strings.ToLower(name)
	if id, ok := cache[key]; ok {
		return id, nil
	}
	// Try to create; on conflict, list and find
	tag, err := w.tags.Create(ctx, uuid.New(), libraryID, name, "", createdBy)
	if err != nil {
		all, listErr := w.tags.List(ctx, libraryID)
		if listErr != nil {
			return uuid.Nil, fmt.Errorf("listing tags: %w", listErr)
		}
		for _, t := range all {
			if strings.EqualFold(t.Name, name) {
				cache[key] = t.ID
				return t.ID, nil
			}
		}
		return uuid.Nil, fmt.Errorf("creating tag %q: %w", name, err)
	}
	cache[key] = tag.ID
	return tag.ID, nil
}

// ─── Genre normalization ──────────────────────────────────────────────────────

// normalizeCategories maps provider category strings against the known genres.
// Splits on "/" and ",", skips strings with ">" or ":", caps at 4.
func normalizeCategories(cats []string, allGenres []*models.Genre) []uuid.UUID {
	byName := make(map[string]*models.Genre, len(allGenres))
	for _, g := range allGenres {
		byName[strings.ToLower(g.Name)] = g
	}

	seen := make(map[uuid.UUID]bool)
	var matched []*models.Genre

	for _, cat := range cats {
		for _, part := range strings.FieldsFunc(cat, func(r rune) bool { return r == '/' || r == ',' }) {
			part = strings.TrimSpace(part)
			if part == "" || strings.Contains(part, ">") || strings.Contains(part, ":") {
				continue
			}
			if g, ok := byName[strings.ToLower(part)]; ok && !seen[g.ID] {
				seen[g.ID] = true
				matched = append(matched, g)
			}
		}
	}

	// Sort by name length ascending (shorter = more general/cleaner)
	for i := 1; i < len(matched); i++ {
		for j := i; j > 0 && len(matched[j].Name) < len(matched[j-1].Name); j-- {
			matched[j], matched[j-1] = matched[j-1], matched[j]
		}
	}
	const maxGenres = 4
	if len(matched) > maxGenres {
		matched = matched[:maxGenres]
	}

	ids := make([]uuid.UUID, len(matched))
	for i, g := range matched {
		ids[i] = g.ID
	}
	return ids
}

// ─── Small helpers ────────────────────────────────────────────────────────────

// knownContributorRoles is the set of valid role strings that may appear in
// parentheses after a contributor name (e.g. "Jane Smith (Illustrator)").
// Must stay in sync with CONTRIBUTOR_ROLES in web/src/components/ContributorRow.tsx.
var knownContributorRoles = map[string]struct{}{
	"author": {}, "artist": {}, "illustrator": {}, "writer": {}, "penciller": {}, "inker": {},
	"colorist": {}, "letterer": {}, "translator": {}, "editor": {}, "narrator": {},
}

// parseContributorNameRole splits "Name (Role)" into ("Name", "role") when the
// parenthetical matches a known role. Otherwise it returns the full string and "author".
func parseContributorNameRole(raw string) (name, role string) {
	raw = strings.TrimSpace(raw)
	// Match trailing "(...)" — must be the last thing in the string.
	open := strings.LastIndex(raw, "(")
	if open > 0 && raw[len(raw)-1] == ')' {
		candidate := strings.ToLower(strings.TrimSpace(raw[open+1 : len(raw)-1]))
		if _, ok := knownContributorRoles[candidate]; ok {
			return strings.TrimSpace(raw[:open]), candidate
		}
	}
	return raw, "author"
}

func splitAuthors(s string) []string {
	var names []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			names = append(names, p)
		}
	}
	return names
}

func findMediaTypeID(types []*models.MediaType, name string) uuid.UUID {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return uuid.Nil // caller handles empty case
	}
	for _, mt := range types {
		if strings.ToLower(mt.DisplayName) == lower || strings.ToLower(mt.Name) == lower {
			return mt.ID
		}
	}
	for _, mt := range types {
		if strings.Contains(strings.ToLower(mt.DisplayName), lower) || strings.Contains(strings.ToLower(mt.Name), lower) {
			return mt.ID
		}
	}
	return uuid.Nil
}

// inferMediaType checks each comma-separated tag against media type names/display-names
// for an exact match — used when no explicit media_type is provided in the CSV.
func inferMediaType(types []*models.MediaType, tags string) uuid.UUID {
	if tags == "" {
		return uuid.Nil
	}
	for _, rawTag := range strings.Split(tags, ",") {
		tag := strings.TrimSpace(strings.ToLower(rawTag))
		if tag == "" {
			continue
		}
		for _, mt := range types {
			if tag == strings.ToLower(mt.Name) || tag == strings.ToLower(mt.DisplayName) {
				return mt.ID
			}
		}
	}
	return uuid.Nil
}
