// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"strings"
	"testing"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
)

func TestGenerateProducesValidToken(t *testing.T) {
	userID := uuid.New()
	nt, err := Generate(userID, "test", nil, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Raw token shape: prefix + 43 base62 chars = 52 total.
	if !strings.HasPrefix(nt.Raw, APITokenPrefix) {
		t.Errorf("raw token missing prefix: %q", nt.Raw)
	}
	wantLen := len(APITokenPrefix) + 43
	if len(nt.Raw) != wantLen {
		t.Errorf("raw token length = %d, want %d", len(nt.Raw), wantLen)
	}

	// Suffix stored = last 4 chars of raw.
	if nt.Token.TokenSuffix != nt.Raw[len(nt.Raw)-4:] {
		t.Errorf("suffix mismatch: stored %q vs raw tail %q",
			nt.Token.TokenSuffix, nt.Raw[len(nt.Raw)-4:])
	}

	// Hash stored must match HashToken(raw) — this is the pact between
	// minting and lookup.
	if nt.Token.TokenHash != HashToken(nt.Raw) {
		t.Errorf("stored hash does not match HashToken output")
	}

	// Scopes default to empty (never nil) so the DB insert is happy with
	// a text[] column NOT NULL DEFAULT '{}'.
	if nt.Token.Scopes == nil {
		t.Errorf("Scopes is nil; should be empty slice when no scopes passed")
	}

	// Stamped fields.
	if nt.Token.UserID != userID {
		t.Errorf("UserID = %v, want %v", nt.Token.UserID, userID)
	}
	if nt.Token.Name != "test" {
		t.Errorf("Name = %q, want %q", nt.Token.Name, "test")
	}
}

func TestGenerateIsUnique(t *testing.T) {
	// Sanity check: two consecutive generations yield distinct tokens.
	// Not a rigorous entropy test, just a guard against accidentally
	// seeding the RNG or caching output.
	a, err := Generate(uuid.New(), "a", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate(uuid.New(), "b", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Raw == b.Raw {
		t.Errorf("two generated tokens are identical")
	}
	if a.Token.TokenHash == b.Token.TokenHash {
		t.Errorf("two generated token hashes are identical")
	}
}

func TestGeneratePreservesScopes(t *testing.T) {
	scopes := []string{"books:read", "loans:read"}
	nt, err := Generate(uuid.New(), "scoped", scopes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nt.Token.Scopes) != 2 ||
		nt.Token.Scopes[0] != "books:read" ||
		nt.Token.Scopes[1] != "loans:read" {
		t.Errorf("Scopes not preserved: got %v", nt.Token.Scopes)
	}
}

func TestHashTokenDeterministic(t *testing.T) {
	// Same input always produces the same hash — the middleware relies on
	// this to look up tokens by hash without storing the raw value.
	raw := "lbrm_pat_abcdef1234567890"
	h1 := HashToken(raw)
	h2 := HashToken(raw)
	if h1 != h2 {
		t.Errorf("HashToken is not deterministic: %q vs %q", h1, h2)
	}
	// Different inputs produce different hashes (guards against constant
	// hashing / accidental stubbing).
	if HashToken(raw) == HashToken(raw+"extra") {
		t.Errorf("HashToken collision on trivially different input")
	}
	// sha256 hex = 64 chars. Not strictly required but catches a
	// regression to a different (shorter) digest.
	if len(h1) != 64 {
		t.Errorf("HashToken length = %d, want 64 (hex-encoded sha256)", len(h1))
	}
}

func TestAPITokenActive(t *testing.T) {
	now := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	past := now.Add(-1 * time.Hour)
	future := now.Add(1 * time.Hour)

	cases := []struct {
		name    string
		tok     models.APIToken
		wantOK  bool
	}{
		{
			name:   "fresh, no expiry",
			tok:    models.APIToken{},
			wantOK: true,
		},
		{
			name:   "not-yet-expired",
			tok:    models.APIToken{ExpiresAt: &future},
			wantOK: true,
		},
		{
			name:   "expired",
			tok:    models.APIToken{ExpiresAt: &past},
			wantOK: false,
		},
		{
			name:   "revoked",
			tok:    models.APIToken{RevokedAt: &past},
			wantOK: false,
		},
		{
			name:   "revoked beats not-yet-expired",
			tok:    models.APIToken{RevokedAt: &past, ExpiresAt: &future},
			wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.tok.Active(now); got != c.wantOK {
				t.Errorf("Active() = %v, want %v", got, c.wantOK)
			}
		})
	}
}
