// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/fireball1725/librarium-api/internal/ai"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/providers"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrAIDisabled signals that a suggestions run was skipped because the AI
// subsystem isn't ready (no active provider, user opted out, etc). Callers
// treat this as a soft no-op — the worker doesn't retry on this error.
var ErrAIDisabled = errors.New("ai suggestions disabled for this user")

// ErrRateLimited signals a user-triggered run was blocked by the per-user
// daily rate limit.
var ErrRateLimited = errors.New("ai suggestions rate limit exceeded")

// ErrAlreadyRunning signals that a run is already active for this user, so
// starting another would just queue a duplicate. The handler maps this to 409.
var ErrAlreadyRunning = errors.New("a suggestions run is already in progress for this user")

// ErrRunCancelled signals the run was cancelled mid-flight. The worker treats
// this as a soft stop — no retry.
var ErrRunCancelled = errors.New("suggestions run cancelled")

// MaxBuyPerUser and MaxReadNextPerUser cap the pool of 'new' suggestions
// carried across runs, per type. When a new run pushes a type's total past
// its cap, the oldest entries of that type are evicted. Keeps the user's
// dashboard from turning into an unbounded backlog while letting each shelf
// (buy / read-next) fill independently.
const (
	MaxBuyPerUser      = 30
	MaxReadNextPerUser = 30
)

// SuggestionsService orchestrates a single pass of the suggestions pipeline:
// load inputs, call the active AI provider, parse output, enrich via metadata
// providers, backfill if buy candidates fell short, persist the batch.
type SuggestionsService struct {
	repo       *repository.AISuggestionsRepo
	books      *repository.BookRepo
	editions   *repository.EditionRepo
	bookSvc    *BookService
	aiRegistry *ai.Registry
	aiSvc      *AIService
	jobSvc     *JobService
	userSvc    *AIUserService
	providers  *ProviderService
	pool       *pgxpool.Pool
}

func NewSuggestionsService(
	pool *pgxpool.Pool,
	repo *repository.AISuggestionsRepo,
	books *repository.BookRepo,
	editions *repository.EditionRepo,
	bookSvc *BookService,
	aiRegistry *ai.Registry,
	aiSvc *AIService,
	jobSvc *JobService,
	userSvc *AIUserService,
	providers *ProviderService,
) *SuggestionsService {
	return &SuggestionsService{
		pool:       pool,
		repo:       repo,
		books:      books,
		editions:   editions,
		bookSvc:    bookSvc,
		aiRegistry: aiRegistry,
		aiSvc:      aiSvc,
		jobSvc:     jobSvc,
		userSvc:    userSvc,
		providers:  providers,
	}
}

// RunForUser executes the full pipeline for a single user and returns the
// persisted run record. Safe to call directly from a River worker or an HTTP
// handler. triggeredBy is one of "scheduler" | "admin" | "user". steering is
// nil for scheduled / admin runs and for unsteered manual runs; when present
// it's persisted on the run row and rendered into the prompt.
func (s *SuggestionsService) RunForUser(ctx context.Context, userID uuid.UUID, triggeredBy string, steering *models.SuggestionSteering) (*models.AISuggestionRun, error) {
	// ── Precondition checks ──────────────────────────────────────────────────
	user, err := s.repo.GetOptedInUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrAIDisabled
	}

	provider := s.aiRegistry.Active()
	if provider == nil {
		return nil, ErrAIDisabled
	}

	cfg, err := s.jobSvc.GetAISuggestionsConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load job config: %w", err)
	}
	// Rate-limit sentinels: -1 unlimited (e.g. local free providers), 0 disables
	// user-triggered runs entirely (scheduled runs still work), positive = cap.
	if triggeredBy == "user" {
		switch {
		case cfg.UserRunRateLimitPerDay < 0:
			// unlimited — skip check
		case cfg.UserRunRateLimitPerDay == 0:
			return nil, ErrRateLimited
		default:
			n, err := s.repo.RunsInLast24h(ctx, userID)
			if err != nil {
				return nil, err
			}
			if n >= cfg.UserRunRateLimitPerDay {
				return nil, ErrRateLimited
			}
		}
	}

	// Don't stack a new run on top of a still-running one for the same user.
	// HTTP handlers also check this up front for immediate feedback; the check
	// here protects the queue + scheduler path against racing enqueues.
	running, err := s.repo.CountRunningRunsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("count running runs: %w", err)
	}
	if running > 0 {
		return nil, ErrAlreadyRunning
	}

	perms, err := s.aiSvc.GetPermissions(ctx)
	if err != nil {
		return nil, fmt.Errorf("load permissions: %w", err)
	}

	titles, err := s.repo.ListLibraryTitles(ctx, user.LibraryID, userID)
	if err != nil {
		return nil, fmt.Errorf("load library titles: %w", err)
	}
	// Require *some* signal — either books or a taste profile. Users with a
	// totally empty account get nothing to work from.
	hasTaste := len(user.TasteProfile) > 2 // "{}" = 2 bytes
	if len(titles) == 0 && !hasTaste {
		return nil, ErrAIDisabled
	}

	blocks, err := s.repo.ListBlocks(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load blocks: %w", err)
	}

	// Load the user's already-surfaced 'new' suggestions so the prompt can tell
	// the model to pick different titles this run. Without this the model
	// deterministically regenerates the same picks every time, the unique index
	// silently drops them, and the user sees no growth in the lists.
	existing, err := s.repo.ListSuggestions(ctx, userID, "", "new", nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("load existing suggestions: %w", err)
	}
	var existingBuy, existingReadNext []string
	for _, e := range existing {
		switch e.Type {
		case "buy":
			existingBuy = append(existingBuy, e.Title)
		case "read_next":
			existingReadNext = append(existingReadNext, e.Title)
		}
	}

	// ── Hydrate steering (if any) ────────────────────────────────────────────
	// Names drive both the prompt copy and the row we persist, so the same
	// lookup serves both needs. An entirely-stale payload (every ID deleted
	// since the ask) collapses to nil here and flows through as an unsteered
	// run rather than silently weighting nothing.
	var hydrated *repository.HydratedSteering
	var steeringJSON []byte
	if steering != nil && !steering.IsEmpty() {
		h, err := s.repo.HydrateSteering(ctx, steering)
		if err != nil {
			return nil, fmt.Errorf("hydrate steering: %w", err)
		}
		if !h.IsEmpty() {
			hydrated = h
			b, err := json.Marshal(steering)
			if err != nil {
				return nil, fmt.Errorf("marshal steering: %w", err)
			}
			steeringJSON = b
		}
	}

	// ── Build prompt ─────────────────────────────────────────────────────────
	info := provider.Info()
	prompt := buildSuggestionsPrompt(titles, user.TasteProfile, blocks, perms, cfg, existingBuy, existingReadNext, hydrated)

	// ── Record run starting ──────────────────────────────────────────────────
	// Stamping the configured model on the run row (and pipeline_start event)
	// lets the admin read the timeline later and see which model produced the
	// output — useful when comparing Ollama model choices or spotting a silent
	// config drift.
	modelID := provider.ConfiguredModel()
	runID, err := s.repo.CreateRun(ctx, userID, triggeredBy, info.Name, modelID, steeringJSON)
	if err != nil {
		return nil, err
	}
	start := time.Now()

	s.emit(ctx, runID, "pipeline_start", map[string]any{
		"triggered_by":    triggeredBy,
		"provider":        info.Name,
		"model":           modelID,
		"library_titles":  len(titles),
		"blocks":          len(blocks),
		"permissions":     perms,
		"max_buy":         cfg.MaxBuyPerUser,
		"max_read_next":   cfg.MaxReadNextPerUser,
		"include_taste":   cfg.IncludeTasteProfile,
	})
	s.emit(ctx, runID, "prompt", map[string]any{
		"pass":       "initial",
		"system":     suggestionsSystemPrompt,
		"prompt":     prompt,
		"max_tokens": 2000,
	})

	// ── Pass 1: generate candidates ──────────────────────────────────────────
	// Thinking models (qwen3, deepseek-r1 via Ollama, Claude with extended
	// thinking) burn through thousands of tokens on reasoning before emitting
	// a single visible character, so admins can tune this per deployment.
	// watchCancellation ensures an admin DELETE actually kills a stuck
	// provider call rather than waiting for the provider's client timeout.
	genCtx, stopWatch := s.watchCancellation(ctx, runID)
	resp, err := provider.Generate(genCtx, ai.GenerateRequest{
		System:    suggestionsSystemPrompt,
		Prompt:    prompt,
		MaxTokens: cfg.MaxTokensInitial,
	})
	stopWatch()
	totalIn, totalOut := 0, 0
	totalCost := 0.0
	if err != nil {
		// If the incoming ctx itself is dead (River killed the worker, admin
		// cancelled via watchCancellation) we can't use it to write the failure
		// row — every repo call would just return "context cancelled". Fall back
		// to a fresh short-lived ctx for the terminal writes.
		writeCtx, writeCancel := terminalWriteCtx(ctx)
		defer writeCancel()
		// If the admin cancelled while we were waiting on the provider, record
		// the failure under the existing 'cancelled' status rather than flipping
		// it back to 'failed'.
		if ccErr := s.checkCancelled(writeCtx, runID); ccErr != nil {
			return nil, ccErr
		}
		s.emit(writeCtx, runID, "error", map[string]any{"stage": "ai_generate_initial", "error": err.Error()})
		_ = s.repo.FinishRun(writeCtx, runID, "failed", err.Error(), totalIn, totalOut, totalCost)
		return nil, fmt.Errorf("ai generate: %w", err)
	}
	totalIn += resp.Usage.InputTokens
	totalOut += resp.Usage.OutputTokens
	totalCost += resp.Usage.EstimatedCostUSD
	s.emit(ctx, runID, "ai_response", map[string]any{
		"pass":             "initial",
		"model":            resp.Usage.ModelID,
		"text":             resp.Text,
		"tokens_in":        resp.Usage.InputTokens,
		"tokens_out":       resp.Usage.OutputTokens,
		"cost_usd":         resp.Usage.EstimatedCostUSD,
		"truncated":        resp.Truncated,
	})

	if err := s.checkCancelled(ctx, runID); err != nil {
		return nil, err
	}

	// Thinking models can consume the entire output cap on reasoning and
	// return an empty reply; non-thinking models can get cut mid-list. Either
	// way the parse below would produce junk, so fail fast with a clear
	// message pointing the admin at the token cap.
	if resp.Truncated {
		msg := fmt.Sprintf("provider stopped at max_tokens (%d) — raise max_tokens_initial in the job config or pick a smaller/faster model", cfg.MaxTokensInitial)
		s.emit(ctx, runID, "error", map[string]any{"stage": "ai_generate_initial", "error": msg, "reason": "max_tokens"})
		_ = s.repo.FinishRun(ctx, runID, "failed", msg, totalIn, totalOut, totalCost)
		return nil, fmt.Errorf("ai generate: %s", msg)
	}

	parsed := ParseSuggestions(resp.Text)
	buyParsed, readNextParsed := splitByHeading(resp.Text, parsed)

	// Seed the dedupe set with titles already in the user's pool so the
	// pipeline doesn't spend enrichment cycles on entries the unique index
	// would silently drop at insert time.
	existingKeys, err := s.repo.ListNewSuggestionKeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list existing suggestion keys: %w", err)
	}

	// ── Pass 2: enrich & filter ──────────────────────────────────────────────
	buyItems, rejectedBuyTitles := s.enrichBuy(ctx, runID, userID, user.LibraryID, buyParsed, cfg.MaxBuyPerUser, existingKeys)
	readNextItems := s.resolveReadNext(ctx, runID, user.LibraryID, titles, readNextParsed, cfg.MaxReadNextPerUser, existingKeys)

	// ── Pass 3: backfill if buy fell short ───────────────────────────────────
	backfillAttempts := 0
	for len(buyItems) < cfg.MaxBuyPerUser && backfillAttempts < 2 && len(rejectedBuyTitles) > 0 {
		if err := s.checkCancelled(ctx, runID); err != nil {
			return nil, err
		}
		backfillAttempts++
		exclusions := strings.Join(rejectedBuyTitles, "\n- ")
		backfill := buildBackfillPrompt(prompt, cfg.MaxBuyPerUser-len(buyItems), exclusions)
		s.emit(ctx, runID, "backfill_prompt", map[string]any{
			"attempt":    backfillAttempts,
			"need":       cfg.MaxBuyPerUser - len(buyItems),
			"exclusions": rejectedBuyTitles,
			"prompt":     backfill,
			"max_tokens": cfg.MaxTokensBackfill,
		})
		bfCtx, bfStop := s.watchCancellation(ctx, runID)
		bfResp, bfErr := provider.Generate(bfCtx, ai.GenerateRequest{
			System:    suggestionsSystemPrompt,
			Prompt:    backfill,
			MaxTokens: cfg.MaxTokensBackfill,
		})
		bfStop()
		if bfErr != nil {
			slog.Warn("ai suggestions backfill failed", "user_id", userID, "attempt", backfillAttempts, "error", bfErr)
			s.emit(ctx, runID, "error", map[string]any{"stage": "ai_generate_backfill", "attempt": backfillAttempts, "error": bfErr.Error()})
			break
		}
		totalIn += bfResp.Usage.InputTokens
		totalOut += bfResp.Usage.OutputTokens
		totalCost += bfResp.Usage.EstimatedCostUSD
		s.emit(ctx, runID, "backfill_response", map[string]any{
			"attempt":    backfillAttempts,
			"model":      bfResp.Usage.ModelID,
			"text":       bfResp.Text,
			"tokens_in":  bfResp.Usage.InputTokens,
			"tokens_out": bfResp.Usage.OutputTokens,
			"cost_usd":   bfResp.Usage.EstimatedCostUSD,
			"truncated":  bfResp.Truncated,
		})
		// Truncated backfill means we can't parse the reply cleanly, so stop
		// iterating rather than burning more provider calls to no effect. The
		// run still completes with whatever the initial pass produced.
		if bfResp.Truncated {
			slog.Warn("ai suggestions backfill truncated", "user_id", userID, "attempt", backfillAttempts, "max_tokens", cfg.MaxTokensBackfill)
			s.emit(ctx, runID, "error", map[string]any{
				"stage":     "ai_generate_backfill",
				"attempt":   backfillAttempts,
				"error":     fmt.Sprintf("backfill stopped at max_tokens (%d)", cfg.MaxTokensBackfill),
				"reason":    "max_tokens",
			})
			break
		}

		bfParsed := ParseSuggestions(bfResp.Text)
		// Seed the dedupe set with the titles we've already accepted in this
		// run so the backfill doesn't re-add them.
		for _, it := range buyItems {
			existingKeys[normalizeTitle(it.Title)] = struct{}{}
		}
		bfItems, bfRejected := s.enrichBuy(ctx, runID, userID, user.LibraryID, bfParsed, cfg.MaxBuyPerUser-len(buyItems), existingKeys)
		buyItems = append(buyItems, bfItems...)
		rejectedBuyTitles = append(rejectedBuyTitles, bfRejected...)
	}

	if err := s.checkCancelled(ctx, runID); err != nil {
		return nil, err
	}

	// ── Persist ──────────────────────────────────────────────────────────────
	all := append([]repository.SuggestionInput{}, buyItems...)
	all = append(all, readNextItems...)
	if err := s.repo.AppendSuggestions(ctx, userID, runID, all, MaxBuyPerUser, MaxReadNextPerUser); err != nil {
		s.emit(ctx, runID, "error", map[string]any{"stage": "persist", "error": err.Error()})
		_ = s.repo.FinishRun(ctx, runID, "failed", err.Error(), totalIn, totalOut, totalCost)
		return nil, err
	}

	if err := s.repo.FinishRun(ctx, runID, "completed", "", totalIn, totalOut, totalCost); err != nil {
		return nil, err
	}

	s.emit(ctx, runID, "pipeline_end", map[string]any{
		"buy_count":       len(buyItems),
		"read_next_count": len(readNextItems),
		"tokens_in":       totalIn,
		"tokens_out":      totalOut,
		"cost_usd":        totalCost,
		"duration_ms":     time.Since(start).Milliseconds(),
		"backfill_passes": backfillAttempts,
	})

	slog.Info("ai suggestions run complete",
		"user_id", userID, "run_id", runID, "buy", len(buyItems), "read_next", len(readNextItems),
		"tokens_in", totalIn, "tokens_out", totalOut, "cost_usd", totalCost,
		"duration_ms", time.Since(start).Milliseconds())

	return &models.AISuggestionRun{
		ID:               runID,
		UserID:           userID,
		TriggeredBy:      triggeredBy,
		ProviderType:     info.Name,
		Status:           "completed",
		TokensIn:         totalIn,
		TokensOut:        totalOut,
		EstimatedCostUSD: totalCost,
		StartedAt:        start,
	}, nil
}

// enrichBuy validates each `buy` candidate against the metadata providers and
// the user's library. Returns the accepted items plus the list of titles that
// were rejected (used for backfill exclusions). Hard-blocks (author/series/
// book) are applied here too. Emits an enrichment_decision event per candidate.
// `seen` is mutated to include every accepted title's normalized key so the
// caller can feed the same set into a subsequent backfill without redos.
func (s *SuggestionsService) enrichBuy(ctx context.Context, runID, userID uuid.UUID, libraryID uuid.UUID, parsed []ParsedSuggestion, max int, seen map[string]struct{}) ([]repository.SuggestionInput, []string) {
	var accepted []repository.SuggestionInput
	var rejected []string
	for _, p := range parsed {
		if len(accepted) >= max {
			break
		}
		decision := map[string]any{
			"title":     p.Title,
			"isbn":      p.ISBN,
			"ai_reason": p.Reason,
		}
		key := normalizeTitle(p.Title)
		if _, dup := seen[key]; dup {
			decision["outcome"] = "rejected"
			decision["reason"] = "duplicate"
			s.emit(ctx, runID, "enrichment_decision", decision)
			continue
		}
		if p.Author != "" {
			decision["author"] = p.Author
		}
		// Reject if the user already owns this book. Done before the ISBN lookup
		// so we don't waste a provider call on a title/ISBN we'll drop anyway.
		owned, err := s.repo.BookExistsInLibrary(ctx, libraryID, p.Title, p.ISBN)
		if err != nil {
			slog.Warn("library existence check failed", "isbn", p.ISBN, "error", err)
			decision["owned_check_error"] = err.Error()
		}
		if owned {
			rejected = append(rejected, p.Title)
			decision["outcome"] = "rejected"
			decision["reason"] = "already_owned"
			s.emit(ctx, runID, "enrichment_decision", decision)
			continue
		}

		// Primary path: if the AI gave us an ISBN, try to resolve it. If that
		// fails OR the returned title doesn't match, fall through to title+author
		// search. Models hallucinate ISBNs far more often than they hallucinate
		// the entire book, so recovering from a bad ISBN is high-value.
		var item *repository.SuggestionInput
		var itemMeta floatingBookMetadata
		var primaryReason string
		if p.ISBN != "" {
			merged, err := s.providers.LookupISBNMerged(ctx, p.ISBN)
			switch {
			case err != nil || merged == nil || merged.Title == nil || merged.Title.Value == "":
				primaryReason = "metadata_lookup_failed"
				if err != nil {
					decision["lookup_error"] = err.Error()
				}
			case !fuzzyTitleMatch(p.Title, merged.Title.Value):
				primaryReason = "title_mismatch"
				decision["metadata_lookup"] = mergedSummary(merged)
			default:
				decision["metadata_lookup"] = mergedSummary(merged)
				it := repository.SuggestionInput{
					Type:      "buy",
					Title:     merged.Title.Value,
					Author:    firstAuthor(merged),
					ISBN:      p.ISBN,
					Reasoning: p.Reason,
				}
				if len(merged.Covers) > 0 {
					it.CoverURL = merged.Covers[0].CoverURL
				}
				item = &it
				itemMeta = floatingBookMetadata{
					Title:       merged.Title.Value,
					ISBN10:      fieldValue(merged.ISBN10),
					ISBN13:      fieldValueOr(merged.ISBN13, p.ISBN),
					Description: fieldValue(merged.Description),
					Publisher:   fieldValue(merged.Publisher),
					PublishDate: fieldValue(merged.PublishDate),
					Language:    fieldValue(merged.Language),
					PageCount:   fieldValuePageCount(merged.PageCount),
				}
				if len(merged.Covers) > 0 {
					itemMeta.CoverURL = merged.Covers[0].CoverURL
				}
				if merged.Subtitle != nil {
					itemMeta.Subtitle = merged.Subtitle.Value
				}
				if merged.Authors != nil && merged.Authors.Value != "" {
					for _, a := range strings.Split(merged.Authors.Value, ",") {
						if trimmed := strings.TrimSpace(a); trimmed != "" {
							itemMeta.Authors = append(itemMeta.Authors, trimmed)
						}
					}
				}
			}
		} else {
			primaryReason = "missing_isbn"
		}

		// Fallback path: title+author search. Only runs when the primary path
		// failed AND the AI gave us an author. Missing author = we can't verify
		// the book actually exists, so we treat it the same as a fully made-up
		// suggestion and reject.
		if item == nil {
			if p.Author == "" {
				rejected = append(rejected, p.Title)
				decision["outcome"] = "rejected"
				decision["reason"] = primaryReason
				if primaryReason == "" {
					decision["reason"] = "missing_author"
				}
				s.emit(ctx, runID, "enrichment_decision", decision)
				continue
			}
			fallback := s.findByTitleAuthor(ctx, p.Title, p.Author)
			if fallback == nil {
				rejected = append(rejected, p.Title)
				decision["outcome"] = "rejected"
				decision["reason"] = "title_author_search_failed"
				decision["primary_reject_reason"] = primaryReason
				s.emit(ctx, runID, "enrichment_decision", decision)
				continue
			}
			// Re-check ownership using the ISBN the fallback actually resolved —
			// the AI's made-up ISBN might have missed a match that the real ISBN
			// now catches.
			if owned2, _ := s.repo.BookExistsInLibrary(ctx, libraryID, fallback.Title, fallback.ISBN13); owned2 {
				rejected = append(rejected, p.Title)
				decision["outcome"] = "rejected"
				decision["reason"] = "already_owned"
				decision["recovered_isbn"] = fallback.ISBN13
				s.emit(ctx, runID, "enrichment_decision", decision)
				continue
			}
			decision["recovered_via"] = "title_author_search"
			decision["primary_reject_reason"] = primaryReason
			decision["recovered_isbn"] = fallback.ISBN13
			decision["recovered_title"] = fallback.Title
			decision["recovered_author"] = strings.Join(fallback.Authors, ", ")
			item = &repository.SuggestionInput{
				Type:      "buy",
				Title:     fallback.Title,
				Author:    firstAuthorFromList(fallback.Authors),
				ISBN:      fallback.ISBN13,
				CoverURL:  fallback.CoverURL,
				Reasoning: p.Reason,
			}
			itemMeta = floatingBookMetadata{
				Title:       fallback.Title,
				Authors:     fallback.Authors,
				ISBN13:      fallback.ISBN13,
				ISBN10:      fallback.ISBN10,
				Description: fallback.Description,
				Publisher:   fallback.Publisher,
				PublishDate: fallback.PublishDate,
				Language:    fallback.Language,
				PageCount:   fallback.PageCount,
				CoverURL:    fallback.CoverURL,
			}
		}

		// Resolve or create a floating book + edition for this buy suggestion
		// so the suggestion has a stable book_id clients can use for detail
		// views. A buy suggestion without a book_id now fails to persist
		// (post-000008 schema change). Populates the full metadata from the
		// provider result so the BookDetailPage renders with author,
		// description, publisher, etc. immediately.
		bookID, editionID, bookErr := s.resolveFloatingBook(ctx, userID, itemMeta)
		if bookErr != nil {
			rejected = append(rejected, p.Title)
			decision["outcome"] = "rejected"
			decision["reason"] = "floating_book_create_failed"
			decision["floating_book_error"] = bookErr.Error()
			s.emit(ctx, runID, "enrichment_decision", decision)
			continue
		}
		item.BookID = &bookID
		if editionID != uuid.Nil {
			item.BookEditionID = &editionID
		}

		seen[normalizeTitle(item.Title)] = struct{}{}
		accepted = append(accepted, *item)
		decision["outcome"] = "accepted"
		s.emit(ctx, runID, "enrichment_decision", decision)
	}
	return accepted, rejected
}

// floatingBookMetadata carries the enrichment result into resolveFloatingBook
// so we can populate the full book + edition + contributors on create rather
// than leaving the row empty until a follow-up enrichment pass.
type floatingBookMetadata struct {
	Title       string
	Subtitle    string
	Description string
	Authors     []string
	Publisher   string
	PublishDate string // free-form; parsed leniently
	Language    string
	PageCount   *int
	ISBN10      string
	ISBN13      string
	CoverURL    string // external URL; downloaded and stored after the book row exists
}

// resolveFloatingBook looks up an existing book + edition by ISBN (global,
// not library-scoped) and returns its IDs. If no edition with this ISBN
// exists, creates a new floating book (no library_books rows) populated
// with every metadata field we already have from the enrichment result —
// description, publisher, contributors (authors as book_contributors rows),
// language, publish date, page count — so the BookDetailPage renders
// something meaningful immediately rather than a bare title.
//
// A "floating" book is one with zero rows in the library_books junction —
// it's a real work in the catalog that simply hasn't been added to any
// library yet. Suggestions-as-books uses this to hang full BookPage
// metadata + BookFinder affordances off a `buy` suggestion.
//
// Cover image download is not handled here — the provider's CoverURL is
// an external URL; copying it into cover storage is a separate concern
// owned by the metadata enrichment worker. Floating books render with
// their title-initial fallback until a library acquisition triggers a
// full enrichment pass.
func (s *SuggestionsService) resolveFloatingBook(ctx context.Context, callerID uuid.UUID, meta floatingBookMetadata) (uuid.UUID, uuid.UUID, error) {
	// Prefer ISBN-13, fall back to ISBN-10 for the global lookup.
	lookupISBN := meta.ISBN13
	if lookupISBN == "" {
		lookupISBN = meta.ISBN10
	}
	if lookupISBN != "" {
		if existing, err := s.editions.FindByISBN(ctx, lookupISBN); err == nil && existing != nil {
			return existing.BookID, existing.ID, nil
		}
	}

	mediaTypeID, err := s.defaultMediaTypeID(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("resolving default media type: %w", err)
	}

	// Resolve author names to contributor IDs up-front (outside the tx so
	// a search/insert there doesn't block the transaction longer than
	// necessary).
	type contribResolve struct {
		id   uuid.UUID
		role string
	}
	var contribs []contribResolve
	for _, name := range meta.Authors {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		c, err := s.findOrCreateContributor(ctx, name)
		if err != nil {
			// Non-fatal — skip this author and keep going. A thin book
			// page beats a failed run.
			slog.Warn("resolving author contributor", "name", name, "error", err)
			continue
		}
		contribs = append(contribs, contribResolve{id: c.ID, role: "author"})
	}

	var publishDate *time.Time
	if meta.PublishDate != "" {
		for _, layout := range []string{"2006-01-02", "2006-01", "2006", "January 2, 2006", "Jan 2, 2006"} {
			if t, perr := time.Parse(layout, meta.PublishDate); perr == nil {
				publishDate = &t
				break
			}
		}
	}

	bookID := uuid.New()
	editionID := uuid.New()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := s.books.Create(ctx, tx, bookID,
		meta.Title, meta.Subtitle, mediaTypeID, meta.Description,
	); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("creating floating book: %w", err)
	}

	for i, c := range contribs {
		if err := s.books.EnsureBookContributor(ctx, tx, bookID, c.id, c.role); err != nil {
			return uuid.Nil, uuid.Nil, fmt.Errorf("linking contributor: %w", err)
		}
		_ = i // display_order is handled by EnsureBookContributor
	}

	if err := s.editions.Create(ctx, tx, editionID, bookID,
		models.EditionFormatPaperback, // placeholder until real enrichment
		meta.Language, "", "", // language, edition_name, narrator
		meta.Publisher, publishDate,
		meta.ISBN10, meta.ISBN13, "", // description lives on book, not edition
		nil,            // duration_seconds
		meta.PageCount, // page_count
		true,           // is_primary
		nil,            // narrator_contributor_id
	); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("creating floating edition: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("commit: %w", err)
	}

	// Fetch the cover after the book row exists. Non-fatal on failure —
	// users can re-enrich later when they add the book to a library.
	// cover_images.created_by FKs to users(id), so we attribute to the user
	// whose suggestion this is rather than uuid.Nil.
	if meta.CoverURL != "" && s.bookSvc != nil {
		if ferr := s.bookSvc.FetchCoverFromURL(ctx, bookID, callerID, meta.CoverURL); ferr != nil {
			slog.Warn("fetching floating-book cover",
				"book_id", bookID, "url", meta.CoverURL, "error", ferr)
		}
	}

	return bookID, editionID, nil
}

// fieldValue returns the string value of a FieldResult, or "" when the
// provider merge didn't agree on one.
func fieldValue(f *providers.FieldResult) string {
	if f == nil {
		return ""
	}
	return f.Value
}

// fieldValueOr returns the FieldResult's value, or a fallback string when the
// field is absent.
func fieldValueOr(f *providers.FieldResult, fallback string) string {
	if f == nil || f.Value == "" {
		return fallback
	}
	return f.Value
}

// fieldValuePageCount parses a FieldResult string as a page count, returning
// a *int or nil when absent/unparseable. The merged page_count field is
// stringified; we re-convert here.
func fieldValuePageCount(f *providers.FieldResult) *int {
	if f == nil || f.Value == "" {
		return nil
	}
	var n int
	if _, err := fmt.Sscanf(f.Value, "%d", &n); err != nil || n <= 0 {
		return nil
	}
	return &n
}

// findOrCreateContributor looks up a contributor by exact name match (case
// insensitive) and returns it, or creates one if none exists.
func (s *SuggestionsService) findOrCreateContributor(ctx context.Context, name string) (*models.Contributor, error) {
	// Lazy contributor repo — we don't want a wiring change just for this
	// one helper; route through the existing BookRepo since we already have
	// it, via raw pool access.
	const findQ = `SELECT id FROM contributors WHERE lower(name) = lower($1) LIMIT 1`
	var pgID uuid.UUID
	err := s.pool.QueryRow(ctx, findQ, name).Scan(&pgID)
	if err == nil {
		return &models.Contributor{ID: pgID, Name: name}, nil
	}
	const insertQ = `INSERT INTO contributors (id, name, sort_name, is_corporate) VALUES ($1, $2, $3, false) RETURNING id`
	newID := uuid.New()
	sortName := DeriveSortName(name)
	if err := s.pool.QueryRow(ctx, insertQ, newID, name, sortName).Scan(&pgID); err != nil {
		return nil, err
	}
	return &models.Contributor{ID: pgID, Name: name}, nil
}

// defaultMediaTypeID returns the id of the "Novel" media type (or the first
// media type in the table if Novel isn't present) for use when creating
// floating books where we don't yet know the format.
func (s *SuggestionsService) defaultMediaTypeID(ctx context.Context) (uuid.UUID, error) {
	types, err := s.books.ListMediaTypes(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	for _, t := range types {
		if strings.EqualFold(t.Name, "novel") {
			return t.ID, nil
		}
	}
	if len(types) > 0 {
		return types[0].ID, nil
	}
	return uuid.Nil, fmt.Errorf("no media types defined")
}

// findByTitleAuthor is the fallback path when ISBN lookup fails or mismatches.
// Returns the first search result that fuzzy-matches the AI-provided title and
// author — this dual check is what guards against "the AI invented both the
// ISBN and the book". Returns nil when no result passes both checks or when no
// provider returned an ISBN-13 (without one we can't dedupe against the user's
// library reliably).
func (s *SuggestionsService) findByTitleAuthor(ctx context.Context, title, author string) *providers.BookResult {
	query := title
	if author != "" {
		query = title + " " + author
	}
	results := s.providers.SearchBooks(ctx, query)
	for _, r := range results {
		if r == nil || r.ISBN13 == "" {
			continue
		}
		if !fuzzyTitleMatch(title, r.Title) {
			continue
		}
		if !fuzzyAuthorMatch(author, strings.Join(r.Authors, " ")) {
			continue
		}
		return r
	}
	return nil
}

func mergedSummary(m *providers.MergedBookResult) map[string]any {
	out := map[string]any{}
	if m.Title != nil {
		out["title"] = m.Title.Value
	}
	if m.Authors != nil {
		out["authors"] = m.Authors.Value
	}
	if len(m.Covers) > 0 {
		out["cover_url"] = m.Covers[0].CoverURL
	}
	return out
}

func firstAuthorFromList(authors []string) string {
	if len(authors) == 0 {
		return ""
	}
	return strings.TrimSpace(authors[0])
}

// resolveReadNext matches the AI's read_next titles back to books already in
// the user's library using normalized title matching. Books not found are
// dropped. The list of library titles (loaded for the prompt) is reused here
// so we don't round-trip the DB for every candidate. Emits a read_next_match
// event per candidate describing the match outcome.
func (s *SuggestionsService) resolveReadNext(ctx context.Context, runID uuid.UUID, _ uuid.UUID, libraryTitles []*repository.LibraryTitle, parsed []ParsedSuggestion, max int, seen map[string]struct{}) []repository.SuggestionInput {
	// Build a normalized-title lookup table once.
	byTitle := make(map[string]*repository.LibraryTitle, len(libraryTitles))
	for _, t := range libraryTitles {
		byTitle[normalizeTitle(t.Title)] = t
	}
	var out []repository.SuggestionInput
	for _, p := range parsed {
		if len(out) >= max {
			break
		}
		event := map[string]any{
			"title":     p.Title,
			"ai_reason": p.Reason,
		}
		norm := normalizeTitle(p.Title)
		hit := byTitle[norm]
		matchKind := "exact"
		if hit == nil {
			// Try the slow path: scan for a partial match.
			for key, t := range byTitle {
				if strings.Contains(key, norm) || strings.Contains(norm, key) {
					hit = t
					matchKind = "partial"
					break
				}
			}
		}
		if hit == nil {
			event["outcome"] = "rejected"
			event["reason"] = "not_in_library"
			s.emit(ctx, runID, "read_next_match", event)
			continue
		}
		// Dedupe against the pool — either a prior run's suggestion or an
		// already-accepted entry earlier in this run's list.
		hitKey := normalizeTitle(hit.Title)
		if _, dup := seen[hitKey]; dup {
			event["outcome"] = "rejected"
			event["reason"] = "duplicate"
			event["matched_title"] = hit.Title
			event["match_kind"] = matchKind
			s.emit(ctx, runID, "read_next_match", event)
			continue
		}
		if hit.ReadStatus == "read" {
			event["outcome"] = "rejected"
			event["reason"] = "already_read"
			event["matched_title"] = hit.Title
			event["match_kind"] = matchKind
			s.emit(ctx, runID, "read_next_match", event)
			continue
		}
		bookID := hit.BookID
		item := repository.SuggestionInput{
			Type:      "read_next",
			BookID:    &bookID,
			Title:     hit.Title,
			Author:    hit.Author,
			Reasoning: p.Reason,
		}
		if hit.HasCover {
			item.CoverURL = fmt.Sprintf("/api/v1/libraries/%s/books/%s/cover?v=%d",
				hit.LibraryID, hit.BookID, hit.UpdatedAt.Unix())
		}
		out = append(out, item)
		seen[hitKey] = struct{}{}
		event["outcome"] = "accepted"
		event["matched_title"] = hit.Title
		event["match_kind"] = matchKind
		event["book_id"] = bookID.String()
		s.emit(ctx, runID, "read_next_match", event)
	}
	return out
}

// terminalWriteCtx returns a context safe to use for terminal DB writes (the
// "failed"/"cancelled" row update and its last event) when the job's own ctx
// may already be cancelled. If the input ctx is still live we just bound it to
// 5s; otherwise we detach from it entirely so the cleanup write still lands.
func terminalWriteCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() != nil {
		return context.WithTimeout(context.Background(), 5*time.Second)
	}
	return context.WithTimeout(ctx, 5*time.Second)
}

// watchCancellation returns a derived context that's cancelled whenever the
// run row is marked 'cancelled' in the DB. Use this around long-running
// provider calls so an admin DELETE actually aborts the in-flight HTTP
// request instead of waiting for the 10-minute client timeout. The returned
// stop function must be called when the guarded call returns — it tears down
// the watcher goroutine regardless of whether cancellation fired.
func (s *SuggestionsService) watchCancellation(ctx context.Context, runID uuid.UUID) (context.Context, func()) {
	derived, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-derived.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				status, err := s.repo.GetRunStatus(ctx, runID)
				if err != nil {
					// Row gone → treat as cancelled; stuck provider call gets aborted.
					cancel()
					return
				}
				if status == "cancelled" {
					cancel()
					return
				}
			}
		}
	}()
	return derived, func() {
		close(done)
		cancel()
	}
}

// checkCancelled re-reads the run's status from the DB. If the admin cancelled
// mid-flight we short-circuit with ErrRunCancelled and emit a marker event so
// the run's event log is self-explanatory. Called between pipeline stages —
// never inside tight loops since each call is a round-trip.
func (s *SuggestionsService) checkCancelled(ctx context.Context, runID uuid.UUID) error {
	status, err := s.repo.GetRunStatus(ctx, runID)
	if err != nil {
		// If we can't see the run row we assume cancellation is safest — the
		// admin may have deleted it. Treat as cancelled rather than plowing on.
		return ErrRunCancelled
	}
	if status == "cancelled" {
		s.emit(ctx, runID, "cancelled", map[string]any{"reason": "admin_request"})
		return ErrRunCancelled
	}
	return nil
}

// emit is a best-effort event writer. Failures are logged but never propagate —
// the pipeline must not fail because observability couldn't write.
func (s *SuggestionsService) emit(ctx context.Context, runID uuid.UUID, eventType string, content any) {
	if err := s.repo.AppendEvent(ctx, runID, eventType, content); err != nil {
		slog.Warn("ai run event emit failed", "run_id", runID, "type", eventType, "error", err)
	}
}

// ─── Prompt construction ─────────────────────────────────────────────────────

const suggestionsSystemPrompt = `You are a thoughtful book recommender for a personal library app.
Recommend books that fit the user's demonstrated tastes and stated preferences.
Always ground "read_next" picks in the provided library — never invent titles for that section.
For "buy" picks, suggest real published books with valid ISBN-13 numbers you are confident about.
Keep reasoning to one sentence focused on why THIS user would enjoy THIS book.`

// buildSuggestionsPrompt assembles the pass-1 prompt. Respects admin
// permissions by only including the data categories they've enabled. When
// steering is non-nil, an explicit "user's ask for THIS run" block is rendered
// up top so the model treats the request as primary signal rather than just
// hints blended into the taste profile.
func buildSuggestionsPrompt(titles []*repository.LibraryTitle, taste []byte, blocks []*models.AIBlockedItem, perms AIPermissions, cfg AISuggestionsJobConfig, alreadyBuy, alreadyReadNext []string, steering *repository.HydratedSteering) string {
	var b strings.Builder
	b.WriteString("I have a personal library and want two kinds of book recommendations.\n\n")

	// ── User request for this run (steered only) ────────────────────────────
	// Rendered first so the model weights this above the passive taste
	// profile. We list only the dimensions the user actually filled in —
	// missing ones aren't mentioned at all rather than shown as empty.
	if !steering.IsEmpty() {
		b.WriteString("## User request for THIS run\n")
		b.WriteString("The user has specifically asked for suggestions weighted toward:\n")
		if len(steering.Authors) > 0 {
			names := make([]string, 0, len(steering.Authors))
			for _, a := range steering.Authors {
				names = append(names, a.Name)
			}
			fmt.Fprintf(&b, "  - Authors: %s\n", strings.Join(names, ", "))
		}
		if len(steering.Series) > 0 {
			names := make([]string, 0, len(steering.Series))
			for _, sr := range steering.Series {
				names = append(names, sr.Name)
			}
			fmt.Fprintf(&b, "  - Series: %s\n", strings.Join(names, ", "))
		}
		if len(steering.Genres) > 0 {
			names := make([]string, 0, len(steering.Genres))
			for _, g := range steering.Genres {
				names = append(names, g.Name)
			}
			fmt.Fprintf(&b, "  - Genres: %s\n", strings.Join(names, ", "))
		}
		if len(steering.Tags) > 0 {
			names := make([]string, 0, len(steering.Tags))
			for _, t := range steering.Tags {
				names = append(names, t.Name)
			}
			fmt.Fprintf(&b, "  - Tags: %s\n", strings.Join(names, ", "))
		}
		if steering.Notes != "" {
			fmt.Fprintf(&b, "  - Notes: %s\n", steering.Notes)
		}
		b.WriteString("\nPrioritise books that match this request. You may still draw on the\n")
		b.WriteString("reading history and taste profile below as secondary signal, but when\n")
		b.WriteString("those conflict with the request above, the request wins. For each\n")
		b.WriteString("suggestion, briefly cite which part of the request it satisfies in\n")
		b.WriteString("the reasoning sentence.\n\n")
	}

	// ── Library summary (high signal, always included when permitted) ───────
	if perms.FullLibrary && len(titles) > 0 {
		summary := summarizeLibrary(titles)
		b.WriteString("## Library summary\n")
		b.WriteString(summary)
		b.WriteString("\n")
	}

	// ── Rated / favourited subset ───────────────────────────────────────────
	if perms.Ratings {
		if loved := filterRatedOrFavourite(titles, 4); len(loved) > 0 {
			b.WriteString("## Books I rated highly or favourited\n")
			for _, t := range loved {
				fmt.Fprintf(&b, "- %s", t.Title)
				if t.Author != "" {
					fmt.Fprintf(&b, " — %s", t.Author)
				}
				if t.Rating != nil {
					fmt.Fprintf(&b, " (rated %d/5)", *t.Rating)
				}
				if t.IsFavorite {
					b.WriteString(" ⭐ favourite")
				}
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
		if disliked := filterRatedAtMost(titles, 2); len(disliked) > 0 {
			b.WriteString("## Books I disliked — avoid similar\n")
			for _, t := range disliked {
				fmt.Fprintf(&b, "- %s", t.Title)
				if t.Author != "" {
					fmt.Fprintf(&b, " — %s", t.Author)
				}
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	// ── Read history ────────────────────────────────────────────────────────
	if perms.ReadingHistory {
		if read := filterByStatus(titles, "read"); len(read) > 0 {
			b.WriteString("## Books I've finished\n")
			for _, t := range limit(read, 80) {
				fmt.Fprintf(&b, "- %s", t.Title)
				if t.Author != "" {
					fmt.Fprintf(&b, " — %s", t.Author)
				}
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	// ── Read-next candidates (always sent when read history is allowed) ─────
	if perms.FullLibrary {
		if unread := filterByStatus(titles, "unread"); len(unread) > 0 {
			b.WriteString("## Books I own but haven't read yet (pick from this list ONLY for read_next)\n")
			for _, t := range limit(unread, 150) {
				fmt.Fprintf(&b, "- %s", t.Title)
				if t.Author != "" {
					fmt.Fprintf(&b, " — %s", t.Author)
				}
				if t.SeriesName != "" {
					fmt.Fprintf(&b, " (%s)", t.SeriesName)
				}
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	// ── Taste profile ───────────────────────────────────────────────────────
	if perms.TasteProfile && cfg.IncludeTasteProfile && len(taste) > 2 {
		b.WriteString("## My taste profile\n")
		b.Write(taste)
		b.WriteString("\n\n")
	}

	// ── Blocks ──────────────────────────────────────────────────────────────
	if len(blocks) > 0 {
		b.WriteString("## Never suggest these\n")
		for _, bl := range blocks {
			switch bl.Scope {
			case "book":
				if bl.Author != "" {
					fmt.Fprintf(&b, "- Book: %q by %s\n", bl.Title, bl.Author)
				} else {
					fmt.Fprintf(&b, "- Book: %q\n", bl.Title)
				}
			case "author":
				fmt.Fprintf(&b, "- Anything by %s\n", bl.Author)
			case "series":
				name := bl.SeriesName
				if name == "" {
					name = "(series id " + bl.SeriesID.String() + ")"
				}
				fmt.Fprintf(&b, "- Any book from the series %s\n", name)
			}
		}
		b.WriteString("\n")
	}

	// ── Already-suggested titles ────────────────────────────────────────────
	// If the user has already been shown these in earlier runs we want fresh
	// picks, not the same list again. We list both buckets together — the AI
	// needs to avoid them regardless of which section they originally landed in.
	if len(alreadyBuy) > 0 || len(alreadyReadNext) > 0 {
		b.WriteString("## Already suggested — pick different titles this run\n")
		for _, t := range alreadyBuy {
			fmt.Fprintf(&b, "- %s\n", t)
		}
		for _, t := range alreadyReadNext {
			fmt.Fprintf(&b, "- %s\n", t)
		}
		b.WriteString("\n")
	}

	// ── Task instructions ──────────────────────────────────────────────────
	// Author is mandatory on buy picks: it's both a signal for the title-search
	// fallback (when the AI's ISBN doesn't resolve) and a cheap hallucination
	// guard — a made-up book is much less likely to survive a title+author match.
	fmt.Fprintf(&b, `## Your task
Return two sections. Each suggestion is on its own line using this exact shape.

For the "Books to buy" section, provide %d suggestions of real published books
I do NOT already own. Each line MUST be:

1. Title — Author — ISBN — one-sentence reason specific to me

All four fields are required. If you're unsure of the ISBN, still include the
author and your best guess — the author name lets the pipeline verify the book
exists even when the ISBN is off. Pick an excellent match for my tastes —
variety across themes is welcome but quality is more important than breadth.

For the "Books to read next" section, pick %d books EXCLUSIVELY from my
"Books I own but haven't read yet" list above. Use the exact title I gave you.
Do NOT include an ISBN or author for this section — just:

1. Title — one-sentence reason

Use this exact output format:

## Books to buy
1. Title — Author — ISBN — reason
...

## Books to read next
1. Title — reason
...
`, cfg.MaxBuyPerUser, cfg.MaxReadNextPerUser)

	return b.String()
}

// buildBackfillPrompt is a minimal prompt asking for N more buy suggestions
// that aren't in the rejection list. It doesn't repeat the full context — the
// provider isn't stateful across calls but the earlier prompt's title list is
// already embedded in the call we make.
func buildBackfillPrompt(original string, need int, rejectedList string) string {
	return fmt.Sprintf(`%s

The following previous suggestions didn't check out (wrong ISBN, already owned,
or otherwise rejected). Please suggest %d different BUY picks and skip these:
- %s

Return ONLY the "## Books to buy" section in the same shape.`, original, need, rejectedList)
}

// splitByHeading splits the parsed list into buy/read_next by the position of
// the "Books to read next" heading in the raw text. This is a line-count
// heuristic — good enough given the prompt constraints.
func splitByHeading(raw string, parsed []ParsedSuggestion) (buy, readNext []ParsedSuggestion) {
	lines := strings.Split(raw, "\n")
	readNextStart := -1
	for i, l := range lines {
		ll := strings.ToLower(l)
		if strings.Contains(ll, "books to read next") ||
			(strings.Contains(ll, "read next") && strings.HasPrefix(strings.TrimSpace(l), "#")) {
			readNextStart = i
			break
		}
	}

	// Walk the parsed list alongside the raw lines, assigning each parsed row
	// to buy or read_next based on whether its source line is above or below
	// the heading.
	if readNextStart < 0 {
		return parsed, nil
	}
	// Find which line each parsed entry came from by scanning in order.
	lineIdx := 0
	for _, p := range parsed {
		for lineIdx < len(lines) {
			l := strings.TrimSpace(lines[lineIdx])
			l = listMarker.ReplaceAllString(l, "")
			// A line matches a parsed row when the title prefix lines up.
			if l != "" && strings.HasPrefix(strings.ToLower(l), strings.ToLower(p.Title)) {
				if lineIdx < readNextStart {
					buy = append(buy, p)
				} else {
					readNext = append(readNext, p)
				}
				lineIdx++
				break
			}
			lineIdx++
		}
	}
	return buy, readNext
}

// ─── Small helpers ────────────────────────────────────────────────────────────

func filterRatedOrFavourite(in []*repository.LibraryTitle, minRating int) []*repository.LibraryTitle {
	var out []*repository.LibraryTitle
	for _, t := range in {
		if t.IsFavorite || (t.Rating != nil && *t.Rating >= minRating) {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := 0, 0
		if out[i].Rating != nil {
			ri = *out[i].Rating
		}
		if out[j].Rating != nil {
			rj = *out[j].Rating
		}
		return ri > rj
	})
	return out
}

func filterRatedAtMost(in []*repository.LibraryTitle, maxRating int) []*repository.LibraryTitle {
	var out []*repository.LibraryTitle
	for _, t := range in {
		if t.Rating != nil && *t.Rating <= maxRating {
			out = append(out, t)
		}
	}
	return out
}

func filterByStatus(in []*repository.LibraryTitle, status string) []*repository.LibraryTitle {
	var out []*repository.LibraryTitle
	for _, t := range in {
		if t.ReadStatus == status {
			out = append(out, t)
		}
	}
	return out
}

func limit[T any](s []T, n int) []T {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func firstAuthor(m *providers.MergedBookResult) string {
	if m == nil || m.Authors == nil {
		return ""
	}
	v := m.Authors.Value
	if v == "" {
		return ""
	}
	if i := strings.Index(v, ","); i > 0 {
		return strings.TrimSpace(v[:i])
	}
	return strings.TrimSpace(v)
}

// summarizeLibrary condenses a large library to a short fact-sheet so we
// don't dump thousands of rows into the prompt.
func summarizeLibrary(titles []*repository.LibraryTitle) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Total books: %d\n", len(titles))

	// Top authors by count.
	authorCount := map[string]int{}
	for _, t := range titles {
		if t.Author != "" {
			authorCount[t.Author]++
		}
	}
	if len(authorCount) > 0 {
		type kv struct {
			k string
			v int
		}
		var sorted []kv
		for k, v := range authorCount {
			sorted = append(sorted, kv{k, v})
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
		b.WriteString("Top authors: ")
		for i, kv := range sorted {
			if i >= 10 {
				break
			}
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s (%d)", kv.k, kv.v)
		}
		b.WriteString("\n")
	}

	// Media-type breakdown.
	mtCount := map[string]int{}
	for _, t := range titles {
		mt := t.MediaType
		if mt == "" {
			mt = "Unknown"
		}
		mtCount[mt]++
	}
	if len(mtCount) > 0 {
		b.WriteString("By media type: ")
		first := true
		for mt, n := range mtCount {
			if !first {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s=%d", mt, n)
			first = false
		}
		b.WriteString("\n")
	}

	// Genre counts.
	genreCount := map[string]int{}
	for _, t := range titles {
		for _, g := range t.GenreNames {
			genreCount[g]++
		}
	}
	if len(genreCount) > 0 {
		type kv struct {
			k string
			v int
		}
		var sorted []kv
		for k, v := range genreCount {
			sorted = append(sorted, kv{k, v})
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
		b.WriteString("Top genres: ")
		for i, kv := range sorted {
			if i >= 10 {
				break
			}
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s (%d)", kv.k, kv.v)
		}
		b.WriteString("\n")
	}

	return b.String()
}
