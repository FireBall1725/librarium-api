// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newOllamaTestServer stands up a fake Ollama that records the Authorization
// header of the last request it saw and answers both endpoints the provider
// calls. Returns the provider pointed at it and a pointer to the captured
// header, so a test can assert on what actually went over the wire rather
// than on the struct field.
func newOllamaTestServer(t *testing.T, cfg map[string]string) (*OllamaProvider, *string) {
	t.Helper()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/chat":
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"},"done_reason":"stop"}`))
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	p := NewOllamaProvider()
	full := map[string]string{"base_url": srv.URL, "model": "llama3"}
	for k, v := range cfg {
		full[k] = v
	}
	p.Configure(full)
	return p, &gotAuth
}

// An Ollama fronted by something that authenticates (Ollama Admin's
// path-preserving /api/proxy, a reverse proxy) rejects an unauthenticated
// call outright, so the key has to reach both endpoints, not just chat.
func TestOllamaSendsBearerTokenOnBothEndpoints(t *testing.T) {
	const key = "oa-test-key"

	t.Run("generate", func(t *testing.T) {
		p, gotAuth := newOllamaTestServer(t, map[string]string{"api_key": key})
		if _, err := p.Generate(context.Background(), GenerateRequest{Prompt: "hi"}); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if want := "Bearer " + key; *gotAuth != want {
			t.Errorf("Authorization = %q, want %q", *gotAuth, want)
		}
	})

	t.Run("list models", func(t *testing.T) {
		p, gotAuth := newOllamaTestServer(t, map[string]string{"api_key": key})
		if _, err := p.ListModels(context.Background()); err != nil {
			t.Fatalf("ListModels: %v", err)
		}
		if want := "Bearer " + key; *gotAuth != want {
			t.Errorf("Authorization = %q, want %q", *gotAuth, want)
		}
	})
}

// A bare Ollama on the LAN has no auth. Sending an empty bearer token would
// be worse than sending nothing, so the header must be absent entirely.
func TestOllamaOmitsAuthorizationWithoutKey(t *testing.T) {
	p, gotAuth := newOllamaTestServer(t, nil)
	if _, err := p.Generate(context.Background(), GenerateRequest{Prompt: "hi"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if *gotAuth != "" {
		t.Errorf("Authorization = %q, want no header at all", *gotAuth)
	}
}

// The key is declared password-typed so GET /admin/ai/providers masks it.
// Without this the settings page would echo the token back in the clear.
func TestOllamaAPIKeyFieldIsPasswordTyped(t *testing.T) {
	var found bool
	for _, f := range NewOllamaProvider().Info().ConfigFields {
		if f.Key != "api_key" {
			continue
		}
		found = true
		if f.Type != "password" {
			t.Errorf("api_key field type = %q, want password", f.Type)
		}
		if f.Required {
			t.Error("api_key must stay optional; a bare Ollama has no key")
		}
	}
	if !found {
		t.Fatal("no api_key config field declared")
	}
}
