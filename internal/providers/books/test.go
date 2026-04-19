// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package books

import (
	"context"

	"github.com/fireball1725/librarium-api/internal/providers"
)

// TestProvider is always enabled and returns a hardcoded result for ISBN "12345".
// Used to verify the ISBN lookup pipeline without hitting external services.
type TestProvider struct {
	base
}

func NewTestProvider() *TestProvider {
	return &TestProvider{base: base{enabled: true}}
}

func (p *TestProvider) Info() providers.ProviderInfo {
	return providers.ProviderInfo{
		Name:         "test",
		DisplayName:  "Test Provider",
		Description:  "Returns a mock result for ISBN \"12345\". Always enabled.",
		RequiresKey:  false,
		Capabilities: []string{providers.CapBookISBN},
	}
}

func (p *TestProvider) Configure(cfg map[string]string) {
	// Always enabled; no configuration needed.
	p.enabled = true
}

func (p *TestProvider) LookupByISBN(_ context.Context, isbn string) (*providers.BookResult, error) {
	if isbn != "12345" {
		return nil, nil
	}
	return &providers.BookResult{
		Provider:        "test",
		ProviderDisplay: "Test Provider",
		Title:           "Test Book",
		Subtitle:        "A book for testing",
		Authors:         []string{"Test Author"},
		Publisher:       "Test Publisher",
		PublishDate:     "2026-01-01",
		ISBN10:          "1234567890",
		ISBN13:          "1234567890123",
		Description:     "This is a test book returned by the built-in test provider.",
		Language:        "en",
	}, nil
}
