// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package manga

import "testing"

// TestMangaDexClientIsBounded — see the BookSearchProvider doc comment in
// internal/providers/provider.go. MangaDex only does series lookups today,
// but a zero-value http.Client never gives up on a hung connection, and
// that's not a property worth relying on staying off the search path.
func TestMangaDexClientIsBounded(t *testing.T) {
	c := NewMangaDexProvider().client
	if c == nil {
		t.Fatal("provider has a nil http.Client")
	}
	if c.Timeout <= 0 {
		t.Error("http.Client has no timeout")
	}
}
