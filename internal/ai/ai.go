// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

// Package ai defines the AI provider plugin system used for book-suggestion
// generation. It follows the same shape as internal/providers (metadata
// providers): each provider implements a minimal capability interface, is
// configured from instance_settings, and is registered at startup.
//
// Unlike metadata providers, only one AI provider is active at a time — the
// admin picks it explicitly. The suggestions worker asks the Registry for the
// active provider and calls Generate once per pass.
package ai

// ConfigField describes a single config input the admin UI should render for a
// provider. Shape varies (Anthropic needs api_key+model; Ollama needs
// base_url+model) so each provider declares its own.
type ConfigField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"` // "password" | "text" | "url" | "model"
	Required    bool   `json:"required"`
	Placeholder string `json:"placeholder,omitempty"`
	HelpText    string `json:"help_text,omitempty"`
	// Options lets a provider suggest model IDs in a dropdown while still
	// allowing free-text entry. Empty means pure free-text.
	Options []string `json:"options,omitempty"`
}

// ProviderInfo describes a provider's static metadata for the admin UI.
type ProviderInfo struct {
	Name         string        `json:"name"`
	DisplayName  string        `json:"display_name"`
	Description  string        `json:"description"`
	HelpText     string        `json:"help_text,omitempty"`
	HelpURL      string        `json:"help_url,omitempty"`
	ConfigFields []ConfigField `json:"config_fields"`
}

// GenerateRequest is the provider-agnostic call shape used by the suggestions
// worker. The worker builds the prompt (library summary, favourites, taste
// profile, exclusions) and hands it to the active provider.
type GenerateRequest struct {
	// System is the system prompt. Optional.
	System string
	// Prompt is the user-facing prompt.
	Prompt string
	// MaxTokens caps the response length. 0 means "use a sensible default".
	MaxTokens int
}

// GenerateResponse is what providers return on success.
type GenerateResponse struct {
	Text  string
	Usage UsageInfo
	// Truncated is set when the provider stopped because it hit the output
	// token cap rather than finishing naturally. Thinking models often run
	// out of tokens mid-reasoning, so the service treats this as a failure
	// rather than silently accepting an empty/partial reply.
	Truncated bool
}

// UsageInfo captures per-call token counts and an estimated USD cost based on
// the provider's pricing table. Ollama returns 0 cost (local inference).
type UsageInfo struct {
	ModelID          string
	InputTokens      int
	OutputTokens     int
	EstimatedCostUSD float64
}
