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
	"regexp"
	"strings"
	"time"
)

const (
	defaultOllamaBaseURL   = "http://localhost:11434"
	defaultOllamaModel     = "llama3"
	defaultOllamaMaxTokens = 4096
	ollamaTimeout          = 10 * time.Minute // local inference can be slow on modest hardware
	ollamaListTimeout      = 5 * time.Second  // /api/tags is a management call — keep UI snappy
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

func (p *OllamaProvider) ConfiguredModel() string { return p.model }

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

	// Ollama can hang for a long time: the model may still be loading, a dead
	// TCP connection from the pool may need to time out, or a thinking model
	// may genuinely be churning. Log the send/receive boundaries so an admin
	// watching `docker compose logs -f api` can see exactly where we're stuck.
	slog.Info("ollama request start", "base_url", p.baseURL, "model", p.model, "prompt_bytes", len(body), "max_tokens", maxTokens)
	reqStart := time.Now()

	resp, err := p.client.Do(httpReq)
	if err != nil {
		slog.Warn("ollama request failed", "model", p.model, "elapsed", time.Since(reqStart), "error", err)
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()
	slog.Info("ollama response headers", "model", p.model, "status", resp.StatusCode, "elapsed", time.Since(reqStart))

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Warn("ollama response read failed", "model", p.model, "elapsed", time.Since(reqStart), "error", err)
		return nil, err
	}
	slog.Info("ollama response complete", "model", p.model, "status", resp.StatusCode, "bytes", len(raw), "elapsed", time.Since(reqStart))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var decoded ollamaChatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("ollama decode: %w", err)
	}

	text := stripThinkTags(decoded.Message.Content)
	// Some thinking models (qwen3, deepseek-r1) return their reasoning in a
	// separate field. If the visible content is empty but the server gave us
	// thinking text, surface that so the test probe isn't misleading.
	if strings.TrimSpace(text) == "" && decoded.Message.Thinking != "" {
		text = decoded.Message.Thinking
	}

	// Ollama is local — cost is always 0. Token counts come from the server
	// when the model reports them.
	return &GenerateResponse{
		Text: text,
		Usage: UsageInfo{
			ModelID:          p.model,
			InputTokens:      decoded.PromptEvalCount,
			OutputTokens:     decoded.EvalCount,
			EstimatedCostUSD: 0,
		},
		Truncated: decoded.DoneReason == "length",
	}, nil
}

// OllamaModel describes one locally-pulled model as reported by /api/tags.
// Fields mirror the subset the UI cares about — name for the dropdown, size
// and modified for a supporting caption, and the details block for future
// phases that want to show quant / parameter count.
type OllamaModel struct {
	Name          string    `json:"name"`
	Size          int64     `json:"size"`
	Modified      time.Time `json:"modified"`
	Digest        string    `json:"digest"`
	Family        string    `json:"family,omitempty"`
	ParameterSize string    `json:"parameter_size,omitempty"`
	Quantization  string    `json:"quantization,omitempty"`
}

// ListModels calls the configured Ollama server's /api/tags endpoint and
// returns the locally-pulled models. Used by the admin UI to replace the
// model-name text input with a dropdown. Uses a short, management-scoped
// timeout — the inference client's 10-minute timeout is wildly wrong here.
func (p *OllamaProvider) ListModels(ctx context.Context) ([]OllamaModel, error) {
	if p.baseURL == "" {
		return nil, fmt.Errorf("ollama provider not configured")
	}

	ctx, cancel := context.WithTimeout(ctx, ollamaListTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama /api/tags: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama /api/tags returned status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var decoded ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("ollama /api/tags decode: %w", err)
	}

	out := make([]OllamaModel, 0, len(decoded.Models))
	for _, m := range decoded.Models {
		out = append(out, OllamaModel{
			Name:          m.Name,
			Size:          m.Size,
			Modified:      m.ModifiedAt,
			Digest:        m.Digest,
			Family:        m.Details.Family,
			ParameterSize: m.Details.ParameterSize,
			Quantization:  m.Details.QuantizationLevel,
		})
	}
	return out, nil
}

// thinkTagRE removes inline <think>…</think> blocks. Thinking models may emit
// reasoning before the reply in a single content string; the JSON-parsing
// suggestion pipeline can't digest those tags.
var thinkTagRE = regexp.MustCompile(`(?is)<think>.*?</think>\s*`)

func stripThinkTags(s string) string {
	return strings.TrimSpace(thinkTagRE.ReplaceAllString(s, ""))
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
	Role     string `json:"role"`
	Content  string `json:"content"`
	Thinking string `json:"thinking,omitempty"`
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
	// DoneReason is "stop" on a natural end, "length" when num_predict was hit.
	DoneReason string `json:"done_reason"`
}

type ollamaTagsResponse struct {
	Models []ollamaTagEntry `json:"models"`
}

type ollamaTagEntry struct {
	Name       string            `json:"name"`
	ModifiedAt time.Time         `json:"modified_at"`
	Size       int64             `json:"size"`
	Digest     string            `json:"digest"`
	Details    ollamaTagsDetails `json:"details"`
}

type ollamaTagsDetails struct {
	Family            string `json:"family"`
	ParameterSize     string `json:"parameter_size"`
	QuantizationLevel string `json:"quantization_level"`
}
