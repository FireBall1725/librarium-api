// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package bundled lists the metadata providers the server ships with, so
// main.go and the tests that hold every provider to a rule share one list.
package bundled

import (
	"github.com/fireball1725/librarium-api/internal/providers"
	"github.com/fireball1725/librarium-api/internal/providers/books"
	"github.com/fireball1725/librarium-api/internal/providers/manga"
)

// All returns a fresh instance of every bundled provider, in registration order.
func All() []providers.MetadataProvider {
	return []providers.MetadataProvider{
		books.NewTestProvider(),
		books.NewOpenLibraryProvider(),
		books.NewGoogleBooksProvider(),
		books.NewISBNdbProvider(),
		books.NewHardcoverProvider(),
		books.NewISFDBProvider(),
		books.NewFinnaProvider(),
		books.NewUPCitemdbProvider(),
		manga.NewMangaDexProvider(),
	}
}
