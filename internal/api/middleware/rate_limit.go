// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/respond"
)

// RateLimiter is an in-memory per-key token-bucket suitable for the
// "block obvious brute-force on a single binary" use case. It is NOT a
// distributed rate limiter — operators running multiple replicas behind a
// load balancer should put rate limiting at the proxy layer instead.
//
// Keys are typically client IPs (recovered via realIP from the logger
// middleware so X-Forwarded-For is honoured behind Traefik). Buckets refill
// at `refill` per `interval`, capped at `burst`.
type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	burst    float64
	refill   float64        // tokens per second
	interval time.Duration  // window used for human-readable config
	idleTTL  time.Duration  // garbage-collect buckets unseen this long
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// NewRateLimiter constructs a limiter that allows `burst` requests in any
// short interval and refills `refill` tokens per `interval`. For example,
// NewRateLimiter(5, 5, time.Minute) → 5/min sustained, 5 burst.
func NewRateLimiter(burst, refill int, interval time.Duration) *RateLimiter {
	return &RateLimiter{
		buckets:  make(map[string]*bucket),
		burst:    float64(burst),
		refill:   float64(refill) / interval.Seconds(),
		interval: interval,
		idleTTL:  10 * time.Minute,
	}
}

// allow returns true if the caller may proceed and decrements the bucket;
// false means the caller is over budget right now.
func (rl *RateLimiter) allow(key string) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.gc(now)

	b, ok := rl.buckets[key]
	if !ok {
		rl.buckets[key] = &bucket{tokens: rl.burst - 1, lastSeen: now}
		return true
	}

	// Refill based on elapsed time, capped at burst.
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens += elapsed * rl.refill
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.lastSeen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// gc evicts buckets that haven't been touched recently. Cheap because we
// hold the mutex anyway and the map fits in memory for any realistic
// per-IP workload.
func (rl *RateLimiter) gc(now time.Time) {
	for k, b := range rl.buckets {
		if now.Sub(b.lastSeen) > rl.idleTTL {
			delete(rl.buckets, k)
		}
	}
}

// Middleware returns an http.Handler wrapper that 429s requests over budget.
// Keying is by client IP (X-Forwarded-For aware via realIP).
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := realIP(r)
		if !rl.allow(key) {
			// Retry-After in seconds gives clients a hint without leaking
			// our exact internal budget.
			w.Header().Set("Retry-After", "60")
			respond.Error(w, http.StatusTooManyRequests, "too many requests; please try again later")
			return
		}
		next.ServeHTTP(w, r)
	})
}
