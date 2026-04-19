// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package respond

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// errContextKey is the private key used to store a handler error in the request context.
type ErrContextKey struct{}

// ErrorHolder is a mutable container placed into the request context by Logger
// so that handler code can attach the real underlying error for TUI display.
type ErrorHolder struct {
	Err error
}

// SetHandlerError stores err in the context so the Logger middleware can pick it
// up after the handler returns and include it in the TUI request sample.
// It is a no-op when called with a context that was not prepared by Logger.
func SetHandlerError(ctx context.Context, err error) {
	if h, ok := ctx.Value(ErrContextKey{}).(*ErrorHolder); ok {
		h.Err = err
	}
}

// GetHandlerError retrieves the error stored by SetHandlerError, if any.
func GetHandlerError(ctx context.Context) error {
	if h, ok := ctx.Value(ErrContextKey{}).(*ErrorHolder); ok {
		return h.Err
	}
	return nil
}

type envelope struct {
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func JSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(envelope{Data: data})
}

func Error(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(envelope{Error: msg})
}

// ServerError logs the real Go error (for TUI debugging and structured logs),
// stores it in the request context so the Logger middleware can surface it,
// then responds with a generic 500 so internal details are not leaked to clients.
func ServerError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "handler error",
		"error", err,
		"method", r.Method,
		"path", r.URL.Path,
	)
	SetHandlerError(r.Context(), err)
	Error(w, http.StatusInternalServerError, "internal server error")
}
