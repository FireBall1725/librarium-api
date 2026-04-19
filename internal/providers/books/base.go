// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

// Package books contains book metadata providers.
package books

// base holds shared enabled/disabled state.
type base struct {
	enabled bool
}

func (b *base) Enabled() bool { return b.enabled }
