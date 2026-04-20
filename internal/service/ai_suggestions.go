// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
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

// MaxSuggestionsPerUser caps the pool of 'new' suggestions carried across
// runs. When a new run pushes the total past this, the oldest entries are
// evicted. Keeps the user's dashboard from turning into an unbounded backlog.
const MaxSuggestionsPerUser = 30

// SuggestionsService orchestrates a single pass of the suggestions pipeline:
// load inputs, call the active AI provider, parse output, enrich via metadata
// providers, backfill if buy candidates fell short, persist the batch.
type SuggestionsService struct {
	repo       *repository.AISuggestionsRepo
	aiRegistry *ai.Registry
	aiSvc      *AIService
	jobSvc     *JobService
	userSvc    *AIUserService
	providers  *ProviderService
	pool       any // retained if we need transactions later
}

func NewSuggestionsService(
	repo *repository.AISuggestionsRepo,
	aiRegistry *ai.Registry,
	aiSvc *AIService,
	jobSvc *JobService,
	userSvc *AIUserService,
	providers *ProviderService,
) *SuggestionsService {
	return &SuggestionsService{
		repo:       repo,
		aiRegistry: aiRegistry,
		aiSvc:      aiSvc,
		jobSvc:     jobSvc,
		userSvc:    userSvc,
		providers:  providers,
	}
}

// RunForUser executes the full pipeline for a single user and returns the
// persisted run record. Safe to call directly from a River worker or an HTTP
// handler. triggeredBy is one of "scheduler" | "admin" | "user".
func (s *SuggestionsService) RunForUser(ctx context.Context, userID uuid.UUID, triggeredBy string) (*models.AISuggestionRun, error) {
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
	if triggeredBy == "user" && cfg.UserRunRateLimitPerDay > 0 {
		n, err := s.repo.RunsInLast24h(ctx, userID)
		if err != nil {
			return nil, err
		}
		if n >= cfg.UserRunRateLimitPerDay {
			return nil, ErrRateLimited
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

	// ── Build prompt ─────────────────────────────────────────────────────────
	info := provider.Info()
	prompt := buildSuggestionsPrompt(titles, user.TasteProfile, blocks, perms, cfg)

	// ── Record run starting ──────────────────────────────────────────────────
	runID, err := s.repo.CreateRun(ctx, userID, triggeredBy, info.Name, "")
	if err != nil {
		return nil, err
	}
	start := time.Now()

	s.emit(ctx, runID, "pipeline_start", map[string]any{
		"triggered_by":    triggeredBy,
		"provider":        info.Name,
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
	resp, err := provider.Generate(ctx, ai.GenerateRequest{
		System:    suggestionsSystemPrompt,
		Prompt:    prompt,
		MaxTokens: cfg.MaxTokensInitial,
	})
	totalIn, totalOut := 0, 0
	totalCost := 0.0
	if err != nil {
		// If the admin cancelled while we were waiting on the provider, record
		// the failure under the existing 'cancelled' status rather than flipping
		// it back to 'failed'.
		if ccErr := s.checkCancelled(ctx, runID); ccErr != nil {
			return nil, ccErr
		}
		s.emit(ctx, runID, "error", map[string]any{"stage": "ai_generate_initial", "error": err.Error()})
		_ = s.repo.FinishRun(ctx, runID, "failed", err.Error(), totalIn, totalOut, totalCost)
		return nil, fmt.Errorf("ai generate: %w", err)
	}
	totalIn += resp.Usage.InputTokens
	totalOut += resp.Usage.OutputTokens
	totalCost += resp.Usage.EstimatedCostUSD
	s.emit(ctx, runID, "ai_response", map[string]any{
		"pass":             "initial",
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
	buyItems, rejectedBuyTitles := s.enrichBuy(ctx, runID, user.LibraryID, buyParsed, cfg.MaxBuyPerUser, existingKeys)
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
		bfResp, bfErr := provider.Generate(ctx, ai.GenerateRequest{
			System:    suggestionsSystemPrompt,
			Prompt:    backfill,
			MaxTokens: cfg.MaxTokensBackfill,
		})
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
		bfItems, bfRejected := s.enrichBuy(ctx, runID, user.LibraryID, bfParsed, cfg.MaxBuyPerUser-len(buyItems), existingKeys)
		buyItems = append(buyItems, bfItems...)
		rejectedBuyTitles = append(rejectedBuyTitles, bfRejected...)
	}

	if err := s.checkCancelled(ctx, runID); err != nil {
		return nil, err
	}

	// ── Persist ──────────────────────────────────────────────────────────────
	all := append([]repository.SuggestionInput{}, buyItems...)
	all = append(all, readNextItems...)
	if err := s.repo.AppendSuggestions(ctx, userID, runID, all, MaxSuggestionsPerUser); err != nil {
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
func (s *SuggestionsService) enrichBuy(ctx context.Context, runID uuid.UUID, libraryID uuid.UUID, parsed []ParsedSuggestion, max int, seen map[string]struct{}) ([]repository.SuggestionInput, []string) {
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
		if p.ISBN == "" {
			rejected = append(rejected, p.Title)
			decision["outcome"] = "rejected"
			decision["reason"] = "missing_isbn"
			s.emit(ctx, runID, "enrichment_decision", decision)
			continue
		}
		// Reject if the user already owns this book.
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
		// Look up via metadata providers. A provider that can't resolve the ISBN
		// likely means the AI hallucinated it — drop the suggestion.
		merged, err := s.providers.LookupISBNMerged(ctx, p.ISBN)
		if err != nil || merged == nil || merged.Title == nil || merged.Title.Value == "" {
			rejected = append(rejected, p.Title)
			decision["outcome"] = "rejected"
			decision["reason"] = "metadata_lookup_failed"
			if err != nil {
				decision["lookup_error"] = err.Error()
			}
			s.emit(ctx, runID, "enrichment_decision", decision)
			continue
		}
		lookup := map[string]any{"title": merged.Title.Value}
		if merged.Authors != nil {
			lookup["authors"] = merged.Authors.Value
		}
		if len(merged.Covers) > 0 {
			lookup["cover_url"] = merged.Covers[0].CoverURL
		}
		decision["metadata_lookup"] = lookup
		// Title fuzzy-match: if the provider-returned title doesn't roughly
		// match the AI's claim, assume ISBN is wrong.
		if !fuzzyTitleMatch(p.Title, merged.Title.Value) {
			rejected = append(rejected, p.Title)
			decision["outcome"] = "rejected"
			decision["reason"] = "title_mismatch"
			s.emit(ctx, runID, "enrichment_decision", decision)
			continue
		}

		item := repository.SuggestionInput{
			Type:      "buy",
			Title:     merged.Title.Value,
			Author:    firstAuthor(merged),
			ISBN:      p.ISBN,
			Reasoning: p.Reason,
		}
		if len(merged.Covers) > 0 {
			item.CoverURL = merged.Covers[0].CoverURL
		}
		// Mark the canonical (provider-returned) title as seen — that's what
		// we'll actually persist.
		seen[normalizeTitle(merged.Title.Value)] = struct{}{}
		accepted = append(accepted, item)
		decision["outcome"] = "accepted"
		s.emit(ctx, runID, "enrichment_decision", decision)
	}
	return accepted, rejected
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
// permissions by only including the data categories they've enabled.
func buildSuggestionsPrompt(titles []*repository.LibraryTitle, taste []byte, blocks []*models.AIBlockedItem, perms AIPermissions, cfg AISuggestionsJobConfig) string {
	var b strings.Builder
	b.WriteString("I have a personal library and want two kinds of book recommendations.\n\n")

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

	// ── Task instructions ──────────────────────────────────────────────────
	fmt.Fprintf(&b, `## Your task
Return two sections. Each suggestion is on its own line using this exact shape:

1. Title — ISBN — one-sentence reason specific to me

For the "Books to buy" section, provide %d suggestions of real published books
I do NOT already own. Each must include a valid ISBN-13. Pick an excellent
match for my tastes — variety across themes is welcome but quality is more
important than breadth.

For the "Books to read next" section, pick %d books EXCLUSIVELY from my
"Books I own but haven't read yet" list above. Use the exact title I gave you.
Do not include an ISBN for this section.

Use this exact output format:

## Books to buy
1. Title — ISBN — reason
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
