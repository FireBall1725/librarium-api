// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	defaultOsaurusBaseURL   = "http://localhost:1337"
	defaultOsaurusMaxTokens = 4096
	osaurusTimeout          = 10 * time.Minute // local inference on Apple Silicon can still take a while for large prompts
	osaurusListTimeout      = 5 * time.Second  // /v1/models is a management call — keep UI snappy
)

// OsaurusProvider calls a local Osaurus server via its OpenAI-compatible
// endpoints (/v1/chat/completions for inference, /v1/models for discovery).
// Osaurus is Apple Silicon-only (MLX backend) but the wire format is pure
// OpenAI, so we reuse the OpenAI request/response types rather than
// duplicating them. No API key by default — the server is local and trusted —
// but we accept one in case the admin is reverse-proxying through a gateway
// that requires a bearer token.
type OsaurusProvider struct {
	base
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func NewOsaurusProvider() *OsaurusProvider {
	return &OsaurusProvider{
		base:    base{enabled: false},
		baseURL: defaultOsaurusBaseURL,
		client:  &http.Client{Timeout: osaurusTimeout},
	}
}

func (p *OsaurusProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        "osaurus",
		DisplayName: "Osaurus",
		Description: "Local LLM inference via Osaurus (Apple Silicon, MLX). No per-request cost.",
		HelpText:    "Point this at a running Osaurus server (default http://localhost:1337). Pick a model from those you've pulled via the Osaurus app. The API-key field is only needed if you're reverse-proxying through an auth gateway — leave blank for a direct local connection.",
		HelpURL:     "https://docs.osaurus.ai/",
		ConfigFields: []ConfigField{
			{Key: "base_url", Label: "Base URL", Type: "url", Required: true, Placeholder: defaultOsaurusBaseURL},
			{
				Key:         "model",
				Label:       "Model",
				Type:        "text",
				Required:    true,
				Placeholder: "llama-3.2-3b-instruct-4bit",
				HelpText:    "Any model you've downloaded in the Osaurus app. The dropdown is populated live from the server's /v1/models.",
			},
			{Key: "api_key", Label: "API key (optional)", Type: "password", Required: false, Placeholder: "only if fronted by an auth proxy"},
		},
	}
}

func (p *OsaurusProvider) Configure(cfg map[string]string) {
	if b, ok := cfg["base_url"]; ok && b != "" {
		p.baseURL = strings.TrimRight(b, "/")
	}
	if p.baseURL == "" {
		p.baseURL = defaultOsaurusBaseURL
	}
	p.apiKey = cfg["api_key"]
	if m, ok := cfg["model"]; ok && m != "" {
		p.model = m
	}
	// Osaurus has no intrinsic cost and no required API key, so enabled is
	// driven purely by the explicit toggle — default on when base_url + model
	// are set, matching Ollama's behaviour.
	if v, ok := cfg["enabled"]; ok {
		p.enabled = v == "true"
	} else {
		p.enabled = p.baseURL != "" && p.model != ""
	}
}

func (p *OsaurusProvider) ConfiguredModel() string { return p.model }

func (p *OsaurusProvider) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	if p.baseURL == "" {
		return nil, fmt.Errorf("osaurus provider not configured")
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultOsaurusMaxTokens
	}

	messages := make([]openAIMessage, 0, 2)
	if req.System != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: req.System})
	}
	messages = append(messages, openAIMessage{Role: "user", Content: req.Prompt})

	body, err := json.Marshal(openAIChatRequest{
		Model:     p.model,
		Messages:  messages,
		MaxTokens: maxTokens,
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	slog.Info("osaurus request start", "base_url", p.baseURL, "model", p.model, "prompt_bytes", len(body), "max_tokens", maxTokens)
	reqStart := time.Now()

	resp, err := p.client.Do(httpReq)
	if err != nil {
		slog.Warn("osaurus request failed", "model", p.model, "elapsed", time.Since(reqStart), "error", err)
		return nil, fmt.Errorf("osaurus request: %w", err)
	}
	defer resp.Body.Close()
	slog.Info("osaurus response headers", "model", p.model, "status", resp.StatusCode, "elapsed", time.Since(reqStart))

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// Osaurus follows OpenAI's error envelope.
		var apiErr openAIErrorEnvelope
		if jerr := json.Unmarshal(raw, &apiErr); jerr == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("osaurus %d: %s", resp.StatusCode, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("osaurus returned status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var decoded openAIChatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("osaurus decode: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return nil, fmt.Errorf("osaurus returned no choices")
	}
	slog.Info("osaurus response content", "model", p.model, "finish_reason", decoded.Choices[0].FinishReason, "content", truncate(decoded.Choices[0].Message.Content, 500))

	// Osaurus runs locally — no monetary cost regardless of model.
	return &GenerateResponse{
		Text: stripThinkTags(stripOsaurusTelemetry(decoded.Choices[0].Message.Content)),
		Usage: UsageInfo{
			ModelID:          p.model,
			InputTokens:      decoded.Usage.PromptTokens,
			OutputTokens:     decoded.Usage.CompletionTokens,
			EstimatedCostUSD: 0,
		},
		Truncated: decoded.Choices[0].FinishReason == "length",
	}, nil
}

// stripOsaurusTelemetry removes the perf-stats tail Osaurus appends to
// /v1/chat/completions content: a U+FFFE non-character followed by
// "stats:<requests>;<tokens_per_sec>". The sentinel is technically a Unicode
// noncharacter so we key off that exact rune rather than the surrounding
// prefix, and trim trailing whitespace after the cut.
func stripOsaurusTelemetry(s string) string {
	if i := strings.IndexRune(s, '\uFFFE'); i >= 0 {
		return strings.TrimRight(s[:i], " \t\r\n")
	}
	return s
}

// OsaurusModel is one model available on the configured Osaurus server. The
// response from /v1/models is the standard OpenAI shape, so we only get an
// id + owner + created timestamp — no size / quant / family like Ollama's
// /api/tags provides.
type OsaurusModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by,omitempty"`
	Created int64  `json:"created,omitempty"`
}

// ListModels calls the configured Osaurus server's /v1/models and returns the
// list the UI should show in the model dropdown. Uses a short, management-
// scoped timeout — the inference client's 10-minute timeout is wildly wrong
// for a discovery call.
func (p *OsaurusProvider) ListModels(ctx context.Context) ([]OsaurusModel, error) {
	if p.baseURL == "" {
		return nil, fmt.Errorf("osaurus provider not configured")
	}

	ctx, cancel := context.WithTimeout(ctx, osaurusListTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		slog.Warn("osaurus /v1/models failed", "base_url", p.baseURL, "error", err)
		return nil, fmt.Errorf("osaurus /v1/models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		slog.Warn("osaurus /v1/models non-200", "base_url", p.baseURL, "status", resp.StatusCode, "body", truncate(string(raw), 200))
		return nil, fmt.Errorf("osaurus /v1/models returned status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var decoded osaurusModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("osaurus /v1/models decode: %w", err)
	}

	out := make([]OsaurusModel, 0, len(decoded.Data))
	for _, m := range decoded.Data {
		out = append(out, OsaurusModel{ID: m.ID, OwnedBy: m.OwnedBy, Created: m.Created})
	}
	return out, nil
}

// ─── Osaurus discovery types ──────────────────────────────────────────────────

type osaurusModelsResponse struct {
	Data []osaurusModelEntry `json:"data"`
}

type osaurusModelEntry struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
	Created int64  `json:"created"`
}
