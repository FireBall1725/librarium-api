// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/respond"
)

// MetricsRecorder can receive per-request metrics; implemented by tui.Collector.
type MetricsRecorder interface {
	RecordRequest(method, path, remoteAddr, client, errMsg string, status int, duration time.Duration)
}

// statusRecorder wraps ResponseWriter to capture the written status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Logger logs method, path, status code, and elapsed time for every request.
// If metrics is non-nil, it also records request metrics for the TUI.
func Logger(next http.Handler, metrics MetricsRecorder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Inject an error holder so handlers can attach real errors via SetHandlerError.
		holder := &respond.ErrorHolder{}
		r = r.WithContext(context.WithValue(r.Context(), respond.ErrContextKey{}, holder))

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		elapsed := time.Since(start)

		ip := realIP(r)
		client := classifyClient(r)

		// Collect the error message (if any) for TUI display.
		errMsg := ""
		if holder.Err != nil {
			errMsg = holder.Err.Error()
		}

		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", elapsed.String(),
			"remote_addr", ip,
			"client", client,
		)
		if metrics != nil {
			metrics.RecordRequest(r.Method, r.URL.Path, ip, client, errMsg, rec.status, elapsed)
		}
	})
}

// realIP extracts the originating IP, respecting X-Forwarded-For / X-Real-IP
// headers set by reverse proxies like Traefik.
func realIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For may be a comma-separated chain; take the leftmost.
		if idx := strings.IndexByte(xff, ','); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// classifyClient returns a short client-type string.
// Apps should set X-Librarium-Client: ios|web|api for precise identification.
func classifyClient(r *http.Request) string {
	if c := r.Header.Get("X-Librarium-Client"); c != "" {
		return strings.ToLower(c)
	}
	ua := r.UserAgent()
	switch {
	case strings.Contains(ua, "CFNetwork"),
		strings.Contains(ua, "iPhone"),
		strings.Contains(ua, "iPad"):
		return "ios"
	case strings.Contains(ua, "Mozilla"),
		strings.Contains(ua, "Chrome"):
		return "web"
	case ua == "":
		return ""
	default:
		return "api"
	}
}
