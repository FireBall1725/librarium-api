// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/fireball1725/librarium-api/internal/ai"
	"github.com/fireball1725/librarium-api/internal/repository"
)

// AI settings keys stored under instance_settings.
// Shape matches the plan in plans/ai-suggestions.md:
//
//	ai:provider:<name>   JSON { api_key?, model?, base_url?, enabled }
//	ai:active_provider   string (one of the registered provider names)
const (
	settingsAIProviderPrefix = "ai:provider:"
	settingsAIActiveProvider = "ai:active_provider"
	settingsAIPermissions    = "ai:permissions"
)

// AIPermissions is the deployment-wide data-access policy the admin sets under
// Connections → AI → Permissions. Combined restrictive-wins with each user's
// opt-in so default-off data stays private unless both sides allow it.
type AIPermissions struct {
	ReadingHistory bool `json:"reading_history"`
	Ratings        bool `json:"ratings"`
	Favourites     bool `json:"favourites"`
	FullLibrary    bool `json:"full_library"`
	TasteProfile   bool `json:"taste_profile"`
}

// defaultAIPermissions is the "unset" state — all categories off. Admin must
// explicitly allow each signal before the worker will send it to any provider.
var defaultAIPermissions = AIPermissions{}

// AIService manages AI provider configuration stored in instance_settings.
// Mirrors ProviderService but for AI providers: config is per-provider JSON,
// and exactly one provider can be "active" at a time.
type AIService struct {
	registry *ai.Registry
	settings *repository.SettingsRepo
}

func NewAIService(registry *ai.Registry, settings *repository.SettingsRepo) *AIService {
	return &AIService{registry: registry, settings: settings}
}

// LoadAll reads every provider's config from the DB, applies it to the live
// registry, and loads the active-provider selection.
func (s *AIService) LoadAll(ctx context.Context) error {
	for _, p := range s.registry.All() {
		cfg, err := s.loadConfig(ctx, p.Info().Name)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return err
		}
		p.Configure(cfg)
	}

	active, err := s.settings.Get(ctx, settingsAIActiveProvider)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if active != "" {
		s.registry.SetActive(active)
	}
	return nil
}

// Registry returns the underlying AI registry.
func (s *AIService) Registry() *ai.Registry {
	return s.registry
}

// GetAllProviderStatus returns info + current config for every AI provider.
// Sensitive fields are masked with "***" so we never echo them back.
func (s *AIService) GetAllProviderStatus(ctx context.Context) ([]AIProviderStatus, error) {
	active := s.registry.ActiveName()

	out := make([]AIProviderStatus, 0, len(s.registry.All()))
	for _, p := range s.registry.All() {
		info := p.Info()
		cfg, err := s.loadConfig(ctx, info.Name)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}

		status := AIProviderStatus{
			Name:         info.Name,
			DisplayName:  info.DisplayName,
			Description:  info.Description,
			HelpText:     info.HelpText,
			HelpURL:      info.HelpURL,
			ConfigFields: info.ConfigFields,
			Enabled:      p.Enabled(),
			Active:       info.Name == active,
			Config:       maskedConfig(info.ConfigFields, cfg),
		}
		status.HasAPIKey = cfg["api_key"] != ""
		out = append(out, status)
	}
	return out, nil
}

// ConfigureProvider saves config to the DB (merged over any existing config)
// and reconfigures the live provider. Unknown providers are rejected.
func (s *AIService) ConfigureProvider(ctx context.Context, name string, cfg map[string]string) error {
	if s.registry.Get(name) == nil {
		return fmt.Errorf("unknown AI provider %q", name)
	}

	merged, err := s.loadConfig(ctx, name)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if merged == nil {
		merged = make(map[string]string)
	}
	maps.Copy(merged, cfg)

	data, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	if err := s.settings.Set(ctx, settingsAIProviderPrefix+name, string(data)); err != nil {
		return err
	}
	s.registry.Configure(name, merged)
	return nil
}

// SetActiveProvider picks which configured provider should serve suggestions.
// Passing "" clears the selection (no AI suggestions will run).
func (s *AIService) SetActiveProvider(ctx context.Context, name string) error {
	if name != "" && s.registry.Get(name) == nil {
		return fmt.Errorf("unknown AI provider %q", name)
	}
	if err := s.settings.Set(ctx, settingsAIActiveProvider, name); err != nil {
		return err
	}
	s.registry.SetActive(name)
	return nil
}

// GetPermissions returns the admin-configured data-access policy. Unset keys
// default to false — the admin must explicitly opt each category in.
func (s *AIService) GetPermissions(ctx context.Context) (AIPermissions, error) {
	raw, err := s.settings.Get(ctx, settingsAIPermissions)
	if errors.Is(err, repository.ErrNotFound) {
		return defaultAIPermissions, nil
	}
	if err != nil {
		return defaultAIPermissions, err
	}
	var perms AIPermissions
	if err := json.Unmarshal([]byte(raw), &perms); err != nil {
		return defaultAIPermissions, fmt.Errorf("decode ai permissions: %w", err)
	}
	return perms, nil
}

// SetPermissions persists the admin's data-access policy.
func (s *AIService) SetPermissions(ctx context.Context, perms AIPermissions) error {
	data, err := json.Marshal(perms)
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, settingsAIPermissions, string(data))
}

// TestProvider makes a cheap probe call to the named provider to confirm the
// API key and model work. Returns the first bit of text the model returned.
func (s *AIService) TestProvider(ctx context.Context, name string) (string, error) {
	p := s.registry.Get(name)
	if p == nil {
		return "", fmt.Errorf("unknown AI provider %q", name)
	}
	if !p.Enabled() {
		return "", fmt.Errorf("provider is disabled — save config and enable it first")
	}
	// Budget is generous because thinking models (qwen3, deepseek-r1, etc.)
	// burn hundreds of tokens on reasoning before emitting the visible reply;
	// a 20-token cap makes their message.content come back empty.
	resp, err := p.Generate(ctx, ai.GenerateRequest{
		Prompt:    "Reply with the single word: ready.",
		MaxTokens: 2048,
	})
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

// ─── Internal ─────────────────────────────────────────────────────────────────

// loadConfig reads the JSON-encoded config for a provider from instance_settings.
func (s *AIService) loadConfig(ctx context.Context, name string) (map[string]string, error) {
	raw, err := s.settings.Get(ctx, settingsAIProviderPrefix+name)
	if err != nil {
		return nil, err
	}
	var cfg map[string]string
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// maskedConfig returns the config map with any password-typed fields replaced
// by "***" so the API never echoes secrets back. Non-password fields pass
// through unchanged so the UI can show which model / base URL is saved.
func maskedConfig(fields []ConfigFieldView, cfg map[string]string) map[string]string {
	if cfg == nil {
		return nil
	}
	sensitive := make(map[string]bool)
	for _, f := range fields {
		if f.Type == "password" {
			sensitive[f.Key] = true
		}
	}
	out := make(map[string]string, len(cfg))
	for k, v := range cfg {
		if sensitive[k] && v != "" {
			out[k] = "***"
		} else {
			out[k] = v
		}
	}
	return out
}

// ─── DTOs ─────────────────────────────────────────────────────────────────────

// ConfigFieldView is a local alias for ai.ConfigField so the API types live in
// service and don't import ai directly in handlers.
type ConfigFieldView = ai.ConfigField

type AIProviderStatus struct {
	Name         string             `json:"name"`
	DisplayName  string             `json:"display_name"`
	Description  string             `json:"description"`
	HelpText     string             `json:"help_text,omitempty"`
	HelpURL      string             `json:"help_url,omitempty"`
	ConfigFields []ConfigFieldView  `json:"config_fields"`
	Enabled      bool               `json:"enabled"`
	Active       bool               `json:"active"`
	HasAPIKey    bool               `json:"has_api_key"`
	Config       map[string]string  `json:"config,omitempty"`
}
