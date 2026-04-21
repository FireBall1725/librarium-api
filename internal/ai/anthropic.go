// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// defaultAnthropicModel is the model used when the admin has not picked one.
const defaultAnthropicModel = "claude-opus-4-7"

// defaultAnthropicMaxTokens is applied when GenerateRequest.MaxTokens == 0.
const defaultAnthropicMaxTokens = 4096

// AnthropicProvider calls the Anthropic Messages API via the official Go SDK.
type AnthropicProvider struct {
	base
	apiKey string
	model  string
	client *anthropic.Client
}

func NewAnthropicProvider() *AnthropicProvider {
	return &AnthropicProvider{
		base:  base{enabled: false},
		model: defaultAnthropicModel,
	}
}

func (p *AnthropicProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        "anthropic",
		DisplayName: "Anthropic",
		Description: "Claude by Anthropic. Strong reasoning, good for nuanced recommendations.",
		HelpText:    "Create an API key at console.anthropic.com → Settings → API keys. The free tier includes credit for testing; production usage is metered.",
		HelpURL:     "https://console.anthropic.com/settings/keys",
		ConfigFields: []ConfigField{
			{Key: "api_key", Label: "API key", Type: "password", Required: true, Placeholder: "sk-ant-..."},
			{
				Key:         "model",
				Label:       "Model",
				Type:        "model",
				Required:    true,
				Placeholder: defaultAnthropicModel,
				HelpText:    "Pick a known model from the list or type one in. Claude Opus gives the best suggestions; Sonnet is cheaper and faster.",
				Options: []string{
					"claude-opus-4-7",
					"claude-opus-4-6",
					"claude-sonnet-4-6",
					"claude-haiku-4-5",
				},
			},
		},
	}
}

func (p *AnthropicProvider) Configure(cfg map[string]string) {
	p.apiKey = cfg["api_key"]
	if m, ok := cfg["model"]; ok && m != "" {
		p.model = m
	}
	if p.model == "" {
		p.model = defaultAnthropicModel
	}
	if v, ok := cfg["enabled"]; ok {
		p.enabled = v == "true" && p.apiKey != ""
	} else {
		p.enabled = p.apiKey != ""
	}
	if p.apiKey != "" {
		client := anthropic.NewClient(option.WithAPIKey(p.apiKey))
		p.client = &client
	} else {
		p.client = nil
	}
}

func (p *AnthropicProvider) ConfiguredModel() string { return p.model }

func (p *AnthropicProvider) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	if p.client == nil {
		return nil, fmt.Errorf("anthropic provider not configured")
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultAnthropicMaxTokens
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: int64(maxTokens),
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(req.Prompt)),
		},
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}

	msg, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic messages: %w", err)
	}

	var textBuf strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			textBuf.WriteString(block.Text)
		}
	}
	text := textBuf.String()

	inTok := int(msg.Usage.InputTokens)
	outTok := int(msg.Usage.OutputTokens)
	return &GenerateResponse{
		Text: text,
		Usage: UsageInfo{
			ModelID:          p.model,
			InputTokens:      inTok,
			OutputTokens:     outTok,
			EstimatedCostUSD: estimateCost(anthropicPricing, p.model, inTok, outTok),
		},
		Truncated: msg.StopReason == "max_tokens",
	}, nil
}
