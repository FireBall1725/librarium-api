// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSecurityHeaders_BasicSet asserts that every response carries the
// fixed set of security headers — HSTS, X-Content-Type-Options,
// X-Frame-Options, Referrer-Policy, Permissions-Policy. A regression in
// any of these would silently relax browser-side protections, so this
// test is deliberately literal.
func TestSecurityHeaders_BasicSet(t *testing.T) {
	t.Parallel()

	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	want := map[string]string{
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Permissions-Policy":        "camera=(), microphone=(), geolocation=(), payment=()",
	}
	for k, v := range want {
		if got := rr.Header().Get(k); got != v {
			t.Errorf("header %q = %q, want %q", k, got, v)
		}
	}
}

// TestSecurityHeaders_CSPDefault verifies that non-docs routes get the
// tight `default-src 'none'` CSP. Anything looser on an API surface that
// only returns JSON would be a real regression.
func TestSecurityHeaders_CSPDefault(t *testing.T) {
	t.Parallel()

	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/libraries", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	csp := rr.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP %q missing default-src 'none'", csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP %q missing frame-ancestors 'none'", csp)
	}
	// Make sure docs-only relaxations didn't leak into the default.
	if strings.Contains(csp, "cdn.jsdelivr.net") {
		t.Errorf("CSP %q should not include the docs-route allowances", csp)
	}
}

// TestSecurityHeaders_CSPDocs documents the deliberate relaxation at the
// `/api/docs` route — Scalar UI loads its bundle from jsdelivr and emits
// inline styles. If the docs surface ever moves, this test surfaces the
// drop in CSP coverage so the fallback is intentional.
func TestSecurityHeaders_CSPDocs(t *testing.T) {
	t.Parallel()

	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	csp := rr.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'self'",
		"https://cdn.jsdelivr.net",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("docs CSP %q missing %q", csp, want)
		}
	}
}
