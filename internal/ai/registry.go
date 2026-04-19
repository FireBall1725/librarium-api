// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package ai

import "sync"

// Registry holds all registered AI providers and remembers which is active.
// Thread-safe; safe for concurrent reads from workers and writes from admin
// config updates.
type Registry struct {
	mu        sync.RWMutex
	providers []SuggestionProvider
	active    string
}

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) Register(p SuggestionProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, p)
}

// All returns every registered provider (active or not).
func (r *Registry) All() []SuggestionProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SuggestionProvider, len(r.providers))
	copy(out, r.providers)
	return out
}

// Get returns the provider with the given name, or nil.
func (r *Registry) Get(name string) SuggestionProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.Info().Name == name {
			return p
		}
	}
	return nil
}

// Configure applies a config map to a single provider by name.
func (r *Registry) Configure(name string, cfg map[string]string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.Info().Name == name {
			p.Configure(cfg)
			return
		}
	}
}

// SetActive records which provider name is the active one.
func (r *Registry) SetActive(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active = name
}

// ActiveName returns the currently-selected provider's name, or "" if unset.
func (r *Registry) ActiveName() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

// Active returns the active provider, or nil if none is selected or the
// selected provider is not enabled.
func (r *Registry) Active() SuggestionProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.Info().Name != r.active {
			continue
		}
		if !p.Enabled() {
			return nil
		}
		return p
	}
	return nil
}
