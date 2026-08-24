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
	// Code is a stable, machine-readable discriminator for the error. Clients
	// branch on it; the message is for people and may be reworded freely.
	// omitempty keeps it absent from every response that does not set one, so
	// nothing already on the wire changes shape.
	Code string `json:"code,omitempty"`
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

// ErrorCode is Error with a machine-readable code attached, for the cases where
// a client has to tell one 4xx apart from another and act differently.
//
// The message still has to read well on its own: the iOS client does not parse
// this envelope on failure, it puts the raw response body into the message the
// user sees.
func ErrorCode(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(envelope{Error: msg, Code: code})
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
