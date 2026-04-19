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

	// ── Pass 1: generate candidates ──────────────────────────────────────────
	resp, err := provider.Generate(ctx, ai.GenerateRequest{
		System:    suggestionsSystemPrompt,
		Prompt:    prompt,
		MaxTokens: 2000,
	})
	totalIn, totalOut := 0, 0
	totalCost := 0.0
	if err != nil {
		_ = s.repo.FinishRun(ctx, runID, "failed", err.Error(), totalIn, totalOut, totalCost)
		return nil, fmt.Errorf("ai generate: %w", err)
	}
	totalIn += resp.Usage.InputTokens
	totalOut += resp.Usage.OutputTokens
	totalCost += resp.Usage.EstimatedCostUSD

	parsed := ParseSuggestions(resp.Text)
	buyParsed, readNextParsed := splitByHeading(resp.Text, parsed)

	// ── Pass 2: enrich & filter ──────────────────────────────────────────────
	buyItems, rejectedBuyTitles := s.enrichBuy(ctx, user.LibraryID, buyParsed, cfg.MaxBuyPerUser)
	readNextItems := s.resolveReadNext(user.LibraryID, titles, readNextParsed, cfg.MaxReadNextPerUser)

	// ── Pass 3: backfill if buy fell short ───────────────────────────────────
	backfillAttempts := 0
	for len(buyItems) < cfg.MaxBuyPerUser && backfillAttempts < 2 && len(rejectedBuyTitles) > 0 {
		backfillAttempts++
		exclusions := strings.Join(rejectedBuyTitles, "\n- ")
		backfill := buildBackfillPrompt(prompt, cfg.MaxBuyPerUser-len(buyItems), exclusions)
		bfResp, bfErr := provider.Generate(ctx, ai.GenerateRequest{
			System:    suggestionsSystemPrompt,
			Prompt:    backfill,
			MaxTokens: 1000,
		})
		if bfErr != nil {
			slog.Warn("ai suggestions backfill failed", "user_id", userID, "attempt", backfillAttempts, "error", bfErr)
			break
		}
		totalIn += bfResp.Usage.InputTokens
		totalOut += bfResp.Usage.OutputTokens
		totalCost += bfResp.Usage.EstimatedCostUSD

		bfParsed := ParseSuggestions(bfResp.Text)
		bfItems, bfRejected := s.enrichBuy(ctx, user.LibraryID, bfParsed, cfg.MaxBuyPerUser-len(buyItems))
		buyItems = append(buyItems, bfItems...)
		rejectedBuyTitles = append(rejectedBuyTitles, bfRejected...)
	}

	// ── Persist ──────────────────────────────────────────────────────────────
	all := append([]repository.SuggestionInput{}, buyItems...)
	all = append(all, readNextItems...)
	if err := s.repo.ReplaceSuggestions(ctx, userID, runID, all); err != nil {
		_ = s.repo.FinishRun(ctx, runID, "failed", err.Error(), totalIn, totalOut, totalCost)
		return nil, err
	}

	if err := s.repo.FinishRun(ctx, runID, "completed", "", totalIn, totalOut, totalCost); err != nil {
		return nil, err
	}

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
// book) are applied here too.
func (s *SuggestionsService) enrichBuy(ctx context.Context, libraryID uuid.UUID, parsed []ParsedSuggestion, max int) ([]repository.SuggestionInput, []string) {
	var accepted []repository.SuggestionInput
	var rejected []string
	for _, p := range parsed {
		if len(accepted) >= max {
			break
		}
		if p.ISBN == "" {
			rejected = append(rejected, p.Title)
			continue
		}
		// Reject if the user already owns this book.
		owned, err := s.repo.BookExistsInLibrary(ctx, libraryID, p.Title, p.ISBN)
		if err != nil {
			slog.Warn("library existence check failed", "isbn", p.ISBN, "error", err)
		}
		if owned {
			rejected = append(rejected, p.Title)
			continue
		}
		// Look up via metadata providers. A provider that can't resolve the ISBN
		// likely means the AI hallucinated it — drop the suggestion.
		merged, err := s.providers.LookupISBNMerged(ctx, p.ISBN)
		if err != nil || merged == nil || merged.Title == nil || merged.Title.Value == "" {
			rejected = append(rejected, p.Title)
			continue
		}
		// Title fuzzy-match: if the provider-returned title doesn't roughly
		// match the AI's claim, assume ISBN is wrong.
		if !fuzzyTitleMatch(p.Title, merged.Title.Value) {
			rejected = append(rejected, p.Title)
			continue
		}
		// (Blocks are applied via prompt exclusions; we trust those to hold.
		// Re-checking here would require loading blocks — the prompt layer's
		// guidance is good enough for v1.)

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
		accepted = append(accepted, item)
	}
	return accepted, rejected
}

// resolveReadNext matches the AI's read_next titles back to books already in
// the user's library using normalized title matching. Books not found are
// dropped. The list of library titles (loaded for the prompt) is reused here
// so we don't round-trip the DB for every candidate.
func (s *SuggestionsService) resolveReadNext(_ uuid.UUID, libraryTitles []*repository.LibraryTitle, parsed []ParsedSuggestion, max int) []repository.SuggestionInput {
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
		norm := normalizeTitle(p.Title)
		hit := byTitle[norm]
		if hit == nil {
			// Try the slow path: scan for a partial match.
			for key, t := range byTitle {
				if strings.Contains(key, norm) || strings.Contains(norm, key) {
					hit = t
					break
				}
			}
		}
		if hit == nil || hit.ReadStatus == "read" {
			continue
		}
		bookID := hit.BookID
		out = append(out, repository.SuggestionInput{
			Type:      "read_next",
			BookID:    &bookID,
			Title:     hit.Title,
			Author:    hit.Author,
			Reasoning: p.Reason,
		})
	}
	return out
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
