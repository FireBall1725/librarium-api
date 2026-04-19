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
	"time"
)

// openAIChatURL is the standard OpenAI Chat Completions endpoint. Not
// configurable — anyone running an OpenAI-compatible local server should use
// the Ollama provider instead.
const openAIChatURL = "https://api.openai.com/v1/chat/completions"

const (
	defaultOpenAIModel     = "gpt-4o-mini"
	defaultOpenAIMaxTokens = 4096
	openAITimeout          = 120 * time.Second
)

// OpenAIProvider calls the OpenAI Chat Completions API over raw HTTP. No SDK
// dependency so the binary stays lean — the endpoint is a single stable POST.
type OpenAIProvider struct {
	base
	apiKey string
	model  string
	client *http.Client
}

func NewOpenAIProvider() *OpenAIProvider {
	return &OpenAIProvider{
		base:   base{enabled: false},
		model:  defaultOpenAIModel,
		client: &http.Client{Timeout: openAITimeout},
	}
}

func (p *OpenAIProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        "openai",
		DisplayName: "OpenAI",
		Description: "GPT models from OpenAI. Good general-purpose suggestions.",
		HelpText:    "Create an API key at platform.openai.com → API keys. Usage is metered — expect a few cents per run depending on model and library size.",
		HelpURL:     "https://platform.openai.com/api-keys",
		ConfigFields: []ConfigField{
			{Key: "api_key", Label: "API key", Type: "password", Required: true, Placeholder: "sk-..."},
			{
				Key:         "model",
				Label:       "Model",
				Type:        "model",
				Required:    true,
				Placeholder: defaultOpenAIModel,
				HelpText:    "gpt-4o-mini is the cheapest current option; gpt-4o gives better suggestions at higher cost.",
				Options: []string{
					"gpt-4o-mini",
					"gpt-4o",
					"gpt-4.1-mini",
					"gpt-4.1",
				},
			},
		},
	}
}

func (p *OpenAIProvider) Configure(cfg map[string]string) {
	p.apiKey = cfg["api_key"]
	if m, ok := cfg["model"]; ok && m != "" {
		p.model = m
	}
	if p.model == "" {
		p.model = defaultOpenAIModel
	}
	if v, ok := cfg["enabled"]; ok {
		p.enabled = v == "true" && p.apiKey != ""
	} else {
		p.enabled = p.apiKey != ""
	}
}

func (p *OpenAIProvider) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("openai provider not configured")
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultOpenAIMaxTokens
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIChatURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// OpenAI error payloads look like {"error":{"message":"...","type":"..."}}
		var apiErr openAIErrorEnvelope
		if jerr := json.Unmarshal(raw, &apiErr); jerr == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("openai %d: %s", resp.StatusCode, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("openai returned status %d", resp.StatusCode)
	}

	var decoded openAIChatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("openai decode: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return nil, fmt.Errorf("openai returned no choices")
	}

	return &GenerateResponse{
		Text: decoded.Choices[0].Message.Content,
		Usage: UsageInfo{
			ModelID:          p.model,
			InputTokens:      decoded.Usage.PromptTokens,
			OutputTokens:     decoded.Usage.CompletionTokens,
			EstimatedCostUSD: estimateCost(openAIPricing, p.model, decoded.Usage.PromptTokens, decoded.Usage.CompletionTokens),
		},
	}, nil
}

// ─── OpenAI API types ─────────────────────────────────────────────────────────

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatRequest struct {
	Model     string          `json:"model"`
	Messages  []openAIMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type openAIErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}
