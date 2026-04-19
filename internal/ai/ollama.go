// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultOllamaBaseURL   = "http://localhost:11434"
	defaultOllamaModel     = "llama3"
	defaultOllamaMaxTokens = 4096
	ollamaTimeout          = 10 * time.Minute // local inference can be slow on modest hardware
)

// OllamaProvider calls a local or self-hosted Ollama instance via /api/chat.
// No API key — the server is trusted by virtue of being on the user's network.
type OllamaProvider struct {
	base
	baseURL string
	model   string
	client  *http.Client
}

func NewOllamaProvider() *OllamaProvider {
	return &OllamaProvider{
		base:    base{enabled: false},
		baseURL: defaultOllamaBaseURL,
		model:   defaultOllamaModel,
		client:  &http.Client{Timeout: ollamaTimeout},
	}
}

func (p *OllamaProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        "ollama",
		DisplayName: "Ollama",
		Description: "Local LLM inference via Ollama. No API key, no per-request cost.",
		HelpText:    "Point this at a running Ollama server (default http://localhost:11434) and name a model you've pulled locally (e.g. llama3, mistral, qwen2).",
		HelpURL:     "https://ollama.com/",
		ConfigFields: []ConfigField{
			{Key: "base_url", Label: "Base URL", Type: "url", Required: true, Placeholder: defaultOllamaBaseURL},
			{
				Key:         "model",
				Label:       "Model",
				Type:        "text",
				Required:    true,
				Placeholder: defaultOllamaModel,
				HelpText:    "Any model you've pulled locally. Run `ollama list` on the Ollama host to see what's available.",
			},
		},
	}
}

func (p *OllamaProvider) Configure(cfg map[string]string) {
	if b, ok := cfg["base_url"]; ok && b != "" {
		p.baseURL = strings.TrimRight(b, "/")
	}
	if p.baseURL == "" {
		p.baseURL = defaultOllamaBaseURL
	}
	if m, ok := cfg["model"]; ok && m != "" {
		p.model = m
	}
	if p.model == "" {
		p.model = defaultOllamaModel
	}
	// Ollama has no API key, so enabled is driven purely by the explicit toggle.
	// Default on when base_url + model are set.
	if v, ok := cfg["enabled"]; ok {
		p.enabled = v == "true"
	} else {
		p.enabled = p.baseURL != "" && p.model != ""
	}
}

func (p *OllamaProvider) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	if p.baseURL == "" {
		return nil, fmt.Errorf("ollama provider not configured")
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultOllamaMaxTokens
	}

	messages := make([]ollamaMessage, 0, 2)
	if req.System != "" {
		messages = append(messages, ollamaMessage{Role: "system", Content: req.System})
	}
	messages = append(messages, ollamaMessage{Role: "user", Content: req.Prompt})

	body, err := json.Marshal(ollamaChatRequest{
		Model:    p.model,
		Messages: messages,
		Stream:   false,
		Options:  ollamaOptions{NumPredict: maxTokens},
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var decoded ollamaChatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("ollama decode: %w", err)
	}

	// Ollama is local — cost is always 0. Token counts come from the server
	// when the model reports them.
	return &GenerateResponse{
		Text: decoded.Message.Content,
		Usage: UsageInfo{
			ModelID:          p.model,
			InputTokens:      decoded.PromptEvalCount,
			OutputTokens:     decoded.EvalCount,
			EstimatedCostUSD: 0,
		},
	}, nil
}

// truncate clips s to at most n runes, appending "…" if truncated. Used for
// error message bodies so we don't pipe an entire HTML error page into logs.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ─── Ollama API types ─────────────────────────────────────────────────────────

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	NumPredict int `json:"num_predict,omitempty"`
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  ollamaOptions   `json:"options"`
}

type ollamaChatResponse struct {
	Message         ollamaMessage `json:"message"`
	PromptEvalCount int           `json:"prompt_eval_count"`
	EvalCount       int           `json:"eval_count"`
}
