// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package middleware

import (
	"context"

	"github.com/fireball1725/librarium-api/internal/api/respond"
)

// SetHandlerError stores err in the context so the Logger middleware can pick it
// up after the handler returns and include it in the TUI request sample.
// It is a no-op when called with a context that was not prepared by Logger.
// Delegates to respond.SetHandlerError to avoid an import cycle.
func SetHandlerError(ctx context.Context, err error) {
	respond.SetHandlerError(ctx, err)
}

