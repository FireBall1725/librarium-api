// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fireball1725/librarium-api/internal/ai"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

// ErrNoActiveAIProvider is returned when an AI metadata operation is requested
// but no provider is configured / enabled. Surfaced to the UI so the toggle
// can be hidden or disabled instead of failing batches silently.
var ErrNoActiveAIProvider = errors.New("no active AI provider configured")

// AIMetadataService runs AI-assisted post-processing on metadata coming back
// from external providers. Each operation records a run row (prompt, response,
// usage) so the jobs UI can expand and inspect the call.
//
// CleanDescription auto-applies — caller writes the cleaned text back. The
// suggestion ops produce a proposal row that the user reviews on the series
// detail page before any structured data is written.
type AIMetadataService struct {
	registry *ai.Registry
	repo     *repository.AIMetadataRepo
}

func NewAIMetadataService(registry *ai.Registry, repo *repository.AIMetadataRepo) *AIMetadataService {
	return &AIMetadataService{registry: registry, repo: repo}
}

// AICallContext is shared bookkeeping every operation needs: who/what is
// triggering this call, so the run row can be linked back to a job and an
// owning library/series in the UI.
type AICallContext struct {
	LibraryID   *uuid.UUID
	JobID       *uuid.UUID
	TriggeredBy *uuid.UUID
}

// CleanDescription strips marketing fluff and normalises formatting on a
// description without changing facts. Returns the cleaned text. The original
// is NOT preserved here — callers that want a revert path should keep their
// own copy before calling.
func (s *AIMetadataService) CleanDescription(ctx context.Context, callCtx AICallContext, targetType string, targetID uuid.UUID, raw string) (string, uuid.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		return raw, uuid.Nil, nil
	}
	provider := s.registry.Active()
	if provider == nil {
		return raw, uuid.Nil, ErrNoActiveAIProvider
	}

	system := `You are an editorial assistant cleaning up book and series descriptions for a library catalog.

You will receive one raw description that may contain:
- marketing fluff ("an instant classic", "a must-read")
- retailer boilerplate ("Ships in 1-2 business days")
- repeated taglines, weird whitespace, HTML remnants
- promotional CTAs ("Get your copy today!")

Return the cleaned version with junk removed and formatting normalised.

CRITICAL RULES — read carefully:
1. DO NOT add any facts, characters, plot points, or details that are not already in the input.
2. DO NOT change titles, character names, place names, dates, or factual claims.
3. DO NOT translate.
4. If the input is already clean, return it nearly unchanged.
5. Output ONLY the cleaned description. No preamble, no explanation, no quotation marks around it.`

	userPrompt := raw

	return s.runText(ctx, callCtx, models.AIMetaKindDescriptionClean, targetType, targetID, provider, system, userPrompt)
}

// SuggestSeriesMetadata asks the AI to propose structured metadata for a
// series (status, total_count, demographic, genres, cleaned description).
// Returns the proposal row id and the umbrella job id created for the call;
// payload lives in ai_metadata_proposals.
func (s *AIMetadataService) SuggestSeriesMetadata(ctx context.Context, callCtx AICallContext, libraryID, seriesID uuid.UUID, name, currentDesc string, currentTotal *int) (uuid.UUID, uuid.UUID, error) {
	provider := s.registry.Active()
	if provider == nil {
		return uuid.Nil, uuid.Nil, ErrNoActiveAIProvider
	}
	if callCtx.JobID == nil {
		jobID, err := s.ensureJob(ctx, callCtx)
		if err != nil {
			return uuid.Nil, uuid.Nil, err
		}
		callCtx.JobID = &jobID
	}

	system := `You are a library cataloging assistant. Given a series name and any available description, propose structured catalog metadata.

Output strict JSON only — no preamble, no explanation, no markdown fences. Match this shape exactly:

{
  "status": "ongoing" | "completed" | "hiatus" | "cancelled" | null,
  "total_count": <integer> | null,
  "demographic": "shounen" | "seinen" | "shoujo" | "josei" | "kodomo" | "young_adult" | "adult" | null,
  "genres": [<string>, ...],
  "description": <cleaned-prose-string> | null
}

Rules:
- Only suggest values you have evidence for. Use null when uncertain.
- demographic only applies to manga / light novels; use null for Western fiction.
- genres should be 1-5 widely-recognised genre names (e.g. "Fantasy", "Slice of Life", "Mystery"). Empty array if uncertain.
- description, when provided, must be a cleaned/concise version of the input — same rules as description cleanup: don't add or remove facts.
- Respond with JSON only.`

	totalStr := "unknown"
	if currentTotal != nil {
		totalStr = fmt.Sprintf("%d", *currentTotal)
	}
	userPrompt := fmt.Sprintf(`Series name: %s
Currently owned volumes: %s
Existing description: %s`, name, totalStr, strings.TrimSpace(currentDesc))

	text, runID, err := s.runText(ctx, callCtx, models.AIMetaKindSeriesMetadata, models.AIMetaTargetSeries, seriesID, provider, system, userPrompt)
	if err != nil {
		return uuid.Nil, *callCtx.JobID, err
	}

	payload, err := parseSeriesMetadataPayload(text)
	if err != nil {
		// Mark the run as failed-with-bad-output but still surface — caller can
		// inspect the prompt/response.
		_ = s.repo.FinishRun(ctx, runID, models.AIMetaRunStatusFailed, fmt.Sprintf("invalid json from model: %v", err), text, 0, 0, 0)
		return uuid.Nil, *callCtx.JobID, fmt.Errorf("parse series metadata payload: %w", err)
	}
	rawJSON, _ := json.Marshal(payload)
	proposalID, err := s.repo.CreateProposal(ctx, libraryID, seriesID, &runID, models.AIMetaTargetSeries, models.AIMetaKindSeriesMetadata, rawJSON)
	if err != nil {
		return uuid.Nil, *callCtx.JobID, err
	}
	return proposalID, *callCtx.JobID, nil
}

// SuggestSeriesArcs asks the AI to propose a canonical arc list for a known
// series. Volume ranges are guesses; user reviews before accepting. Returns
// the proposal id and the umbrella job id created for the call.
func (s *AIMetadataService) SuggestSeriesArcs(ctx context.Context, callCtx AICallContext, libraryID, seriesID uuid.UUID, name string, totalVolumes *int) (uuid.UUID, uuid.UUID, error) {
	provider := s.registry.Active()
	if provider == nil {
		return uuid.Nil, uuid.Nil, ErrNoActiveAIProvider
	}
	if callCtx.JobID == nil {
		jobID, err := s.ensureJob(ctx, callCtx)
		if err != nil {
			return uuid.Nil, uuid.Nil, err
		}
		callCtx.JobID = &jobID
	}

	system := `You are a manga / comics / fiction-series cataloging assistant. Propose the major story-arc breakdown for a series — the saga / arc grouping a reader would use to organise their physical library, NOT a chapter-level or episode-level micro-breakdown.

Output strict JSON only — no preamble, no explanation, no markdown fences. Match this shape exactly:

{
  "arcs": [
    { "name": <string>, "position": <integer>, "vol_start": <integer> | null, "vol_end": <integer> | null }
  ]
}

Rules on granularity:
- Use the SAGA breakdown that long-time readers and official Viz / Shueisha / publisher branding use, not the micro-arcs from a per-fight wiki list.
- The total count scales with series length. Short series may have 4–6 arcs; mid-length 50-volume series usually 5–10 sagas; long-running series (One Piece, Naruto, Bleach, Gintama) typically have 10–15 canonical sagas. Cover the WHOLE series — every volume should fall under exactly one arc unless you genuinely can't place it.
- Do NOT compress or omit canonical sagas to hit a small number. If the standard breakdown for this series is 11 sagas, return 11.
- Do NOT explode into 20+ micro-arcs by listing every named fight. The unit is the SAGA / multi-volume arc, not the chapter or episode.
- For series with both "saga" and "arc" naming layers, prefer the saga layer. Saga-level entries typically span 5–20 volumes each.

Rules on volume ranges:
- vol_start / vol_end should cover the whole series end-to-end when you have confident knowledge — don't truncate at a saga you remember and stop. Use null on either bound only when genuinely uncertain.
- Volume ranges MUST NOT overlap. Each volume belongs to at most one arc. If two arcs share a boundary volume, decide which one it primarily belongs to.
- Return arcs in publication order, position starting at 1.

Other rules:
- Use the most widely-recognised names from official publishers or fandom consensus (Viz / Shueisha for shounen jump titles, official saga names from the publisher's spine treatment, etc.).
- If the series is not widely known, has no documented arcs, or is too short (< 5 volumes), return {"arcs": []}.
- Respond with JSON only.`

	totalStr := "unknown"
	if totalVolumes != nil {
		totalStr = fmt.Sprintf("%d", *totalVolumes)
	}
	userPrompt := fmt.Sprintf(`Series name: %s
Total volumes: %s`, name, totalStr)

	text, runID, err := s.runText(ctx, callCtx, models.AIMetaKindSeriesArcs, models.AIMetaTargetSeries, seriesID, provider, system, userPrompt)
	if err != nil {
		return uuid.Nil, *callCtx.JobID, err
	}

	payload, err := parseSeriesArcsPayload(text)
	if err != nil {
		_ = s.repo.FinishRun(ctx, runID, models.AIMetaRunStatusFailed, fmt.Sprintf("invalid json from model: %v", err), text, 0, 0, 0)
		return uuid.Nil, *callCtx.JobID, fmt.Errorf("parse series arcs payload: %w", err)
	}
	rawJSON, _ := json.Marshal(payload)
	proposalID, err := s.repo.CreateProposal(ctx, libraryID, seriesID, &runID, models.AIMetaTargetSeries, models.AIMetaKindSeriesArcs, rawJSON)
	if err != nil {
		return uuid.Nil, *callCtx.JobID, err
	}
	return proposalID, *callCtx.JobID, nil
}

// ensureJob inserts an umbrella jobs row for synchronous AI calls (the
// suggest-* endpoints) so they appear in unified jobs history. Returns the
// new job id; FinishRun will mirror the final status onto it.
func (s *AIMetadataService) ensureJob(ctx context.Context, callCtx AICallContext) (uuid.UUID, error) {
	if callCtx.TriggeredBy == nil {
		return uuid.Nil, fmt.Errorf("triggered_by required for synchronous AI call")
	}
	return s.repo.CreateJob(ctx, *callCtx.TriggeredBy)
}

// runText is the shared "create run, call provider, finish run" loop. Returns
// the raw response text and the run id (so callers can attach the response to
// a proposal row).
func (s *AIMetadataService) runText(ctx context.Context, callCtx AICallContext, kind, targetType string, targetID uuid.UUID, provider ai.SuggestionProvider, system, userPrompt string) (string, uuid.UUID, error) {
	fullPrompt := system + "\n\n---\n\n" + userPrompt
	runID, err := s.repo.CreateRun(ctx, callCtx.LibraryID, callCtx.JobID, callCtx.TriggeredBy, kind, targetType, targetID, provider.Info().Name, provider.ConfiguredModel(), fullPrompt)
	if err != nil {
		return "", uuid.Nil, err
	}
	resp, err := provider.Generate(ctx, ai.GenerateRequest{System: system, Prompt: userPrompt, MaxTokens: 0})
	if err != nil {
		_ = s.repo.FinishRun(ctx, runID, models.AIMetaRunStatusFailed, err.Error(), "", 0, 0, 0)
		return "", runID, err
	}
	if resp.Truncated {
		_ = s.repo.FinishRun(ctx, runID, models.AIMetaRunStatusFailed, "model output truncated", resp.Text, resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.EstimatedCostUSD)
		return "", runID, fmt.Errorf("model output truncated")
	}
	if err := s.repo.FinishRun(ctx, runID, models.AIMetaRunStatusCompleted, "", resp.Text, resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.EstimatedCostUSD); err != nil {
		return "", runID, err
	}
	return resp.Text, runID, nil
}

// parseSeriesMetadataPayload tolerates the JSON being wrapped in ``` fences
// or having leading/trailing prose despite the strict prompt — small models
// don't always obey perfectly.
func parseSeriesMetadataPayload(raw string) (*models.SeriesMetadataPayload, error) {
	cleaned := stripJSONFences(raw)
	var p models.SeriesMetadataPayload
	if err := json.Unmarshal([]byte(cleaned), &p); err != nil {
		return nil, err
	}
	if p.Genres == nil {
		p.Genres = []string{}
	}
	return &p, nil
}

func parseSeriesArcsPayload(raw string) (*models.SeriesArcsPayload, error) {
	cleaned := stripJSONFences(raw)
	var p models.SeriesArcsPayload
	if err := json.Unmarshal([]byte(cleaned), &p); err != nil {
		return nil, err
	}
	if p.Arcs == nil {
		p.Arcs = []models.ProposedArc{}
	}
	return &p, nil
}

// stripJSONFences removes ```json ... ``` and trims surrounding prose around
// the first/last brace — defence in depth against models that ignore "no
// markdown fences" instructions.
func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	// Remove markdown code fence wrappers if present.
	if strings.HasPrefix(s, "```") {
		// Drop everything up to the first newline (the fence + optional language).
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	// Trim to outer braces in case there's chatter around the JSON.
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "}"); j >= 0 && j < len(s)-1 {
		s = s[:j+1]
	}
	return strings.TrimSpace(s)
}
