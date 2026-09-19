// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package books

import (
	"testing"

	"github.com/fireball1725/librarium-api/internal/providers"
)

// The admin catalogue searches and filters on these, so a provider without
// them can't be found there.
func TestProvidersFillInCatalogueDetails(t *testing.T) {
	for _, p := range []providers.MetadataProvider{
		NewOpenLibraryProvider(), NewGoogleBooksProvider(), NewISBNdbProvider(),
		NewHardcoverProvider(), NewISFDBProvider(), NewFinnaProvider(), NewUPCitemdbProvider(),
	} {
		info := p.Info()
		if info.Region == "" || info.Sends == "" || info.DocsURL == "" {
			t.Errorf("%s: region %q, sends %q, docs %q", info.Name, info.Region, info.Sends, info.DocsURL)
		}
		if info.Kind != "" && info.Kind != providers.KindData && info.Kind != providers.KindBuy {
			t.Errorf("%s: unknown kind %q", info.Name, info.Kind)
		}
	}
}
