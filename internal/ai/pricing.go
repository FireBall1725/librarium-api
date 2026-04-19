// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package ai

// pricePer1M is input/output USD per 1M tokens for a given model.
type pricePer1M struct {
	InUSD  float64
	OutUSD float64
}

// anthropicPricing is a best-effort table of known Anthropic models. Unknown
// models return 0 cost — the admin UI surfaces this as "cost tracking
// unavailable" rather than guessing.
var anthropicPricing = map[string]pricePer1M{
	"claude-opus-4-7":   {InUSD: 5.00, OutUSD: 25.00},
	"claude-opus-4-6":   {InUSD: 5.00, OutUSD: 25.00},
	"claude-opus-4-5":   {InUSD: 5.00, OutUSD: 25.00},
	"claude-sonnet-4-6": {InUSD: 3.00, OutUSD: 15.00},
	"claude-sonnet-4-5": {InUSD: 3.00, OutUSD: 15.00},
	"claude-haiku-4-5":  {InUSD: 1.00, OutUSD: 5.00},
}

// openAIPricing is a best-effort table of known OpenAI models. Prices drift
// over time — the admin UI can show "cost tracking unavailable" rather than
// guess for unknown models. Numbers as of early 2026.
var openAIPricing = map[string]pricePer1M{
	"gpt-4o":       {InUSD: 2.50, OutUSD: 10.00},
	"gpt-4o-mini":  {InUSD: 0.15, OutUSD: 0.60},
	"gpt-4.1":      {InUSD: 2.00, OutUSD: 8.00},
	"gpt-4.1-mini": {InUSD: 0.40, OutUSD: 1.60},
}

// estimateCost computes the USD cost for a generation given token counts and
// the provider's pricing table. Returns 0 when the model is unknown.
func estimateCost(table map[string]pricePer1M, model string, inTokens, outTokens int) float64 {
	p, ok := table[model]
	if !ok {
		return 0
	}
	return (float64(inTokens)/1_000_000.0)*p.InUSD + (float64(outTokens)/1_000_000.0)*p.OutUSD
}
