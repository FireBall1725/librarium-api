// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package ai

import "context"

// SuggestionProvider is the single capability interface AI providers implement.
// Generate sends one prompt and returns the model's text response plus usage.
// The worker is responsible for building the prompt and parsing the text.
type SuggestionProvider interface {
	Info() ProviderInfo
	Configure(cfg map[string]string)
	Enabled() bool
	// ConfiguredModel returns the model ID this provider will generate with at
	// its current configuration. Known before any Generate call, so the worker
	// can stamp it on the run row and timeline events. Empty if the provider
	// hasn't been configured yet.
	ConfiguredModel() string
	Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)
}

// base holds shared enabled state, mirroring the metadata-provider base.
type base struct {
	enabled bool
}

func (b *base) Enabled() bool { return b.enabled }
