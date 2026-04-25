// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRateLimiter_BurstThenBlocks asserts the bucket allows exactly
// `burst` requests in quick succession, then 429s every subsequent
// request. This is the load-bearing behaviour for protecting
// /auth/login from brute-force, so it gets a literal assertion.
func TestRateLimiter_BurstThenBlocks(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter(5, 5, time.Minute)
	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, rr.Code)
		}
	}

	// The 6th call exhausts the burst.
	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("over-budget request: status = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("over-budget response missing Retry-After header")
	}
}

// TestRateLimiter_PerKeyIsolation verifies that one client running into
// the limit doesn't affect a different client IP. Without per-key
// isolation the limiter would amount to a global brake on the auth
// endpoints — useless against credential stuffing.
func TestRateLimiter_PerKeyIsolation(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter(2, 2, time.Minute)
	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Drain client A.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("clientA req %d: status = %d", i+1, rr.Code)
		}
	}
	// Confirm A is now blocked.
	{
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("clientA over-budget: status = %d, want 429", rr.Code)
		}
	}
	// Client B comes in fresh and gets full burst.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = "10.0.0.2:5678"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("clientB req %d: status = %d, want 200 (per-key isolation broken)", i+1, rr.Code)
		}
	}
}

// TestRateLimiter_XForwardedFor exercises the realIP path that the
// limiter shares with the logger middleware. Without it the limiter
// would key every request to the proxy's IP behind Traefik, which is
// effectively a global rate limit.
func TestRateLimiter_XForwardedFor(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter(1, 1, time.Minute)
	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	send := func(xff string) int {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = "10.0.0.99:1111" // proxy
		req.Header.Set("X-Forwarded-For", xff)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr.Code
	}

	if s := send("203.0.113.10"); s != http.StatusOK {
		t.Fatalf("first req from 203.0.113.10: status = %d, want 200", s)
	}
	if s := send("203.0.113.10"); s != http.StatusTooManyRequests {
		t.Errorf("second req from 203.0.113.10: status = %d, want 429", s)
	}
	// A different real IP behind the same proxy still gets its budget.
	if s := send("203.0.113.20"); s != http.StatusOK {
		t.Errorf("first req from 203.0.113.20: status = %d, want 200 (XFF not honoured)", s)
	}
}

// TestRateLimiter_Refills locks in the refill behaviour so a slow
// trickle of requests over the configured window doesn't trip 429.
// Uses a tight interval to keep the test fast.
func TestRateLimiter_Refills(t *testing.T) {
	t.Parallel()

	// burst=1 with 1 token / 50ms refill — easy to exercise without
	// flaky sleeps.
	rl := NewRateLimiter(1, 1, 50*time.Millisecond)
	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	send := func() int {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr.Code
	}

	if s := send(); s != http.StatusOK {
		t.Fatalf("first req: status = %d, want 200", s)
	}
	if s := send(); s != http.StatusTooManyRequests {
		t.Fatalf("second req (no wait): status = %d, want 429", s)
	}
	time.Sleep(80 * time.Millisecond)
	if s := send(); s != http.StatusOK {
		t.Errorf("third req (after refill): status = %d, want 200", s)
	}
}
