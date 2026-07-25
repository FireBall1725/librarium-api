// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"reflect"
	"testing"

	"github.com/fireball1725/librarium-api/internal/providers"
)

func TestMaskProviderConfig(t *testing.T) {
	fieldsCfg := providers.ProviderInfo{
		ConfigFields: []providers.ConfigField{
			{Key: "base_url", Type: "url"},
			{Key: "api_key", Type: "password"},
		},
	}

	cases := []struct {
		name    string
		info    providers.ProviderInfo
		cfg     map[string]string
		wantCfg map[string]string
		wantKey bool
	}{
		{
			name:    "no config fields, no api_key set — legacy path, nothing saved",
			info:    providers.ProviderInfo{},
			cfg:     map[string]string{},
			wantCfg: nil,
			wantKey: false,
		},
		{
			name:    "no config fields, api_key set — legacy path masks it",
			info:    providers.ProviderInfo{},
			cfg:     map[string]string{"api_key": "sk-secret"},
			wantCfg: map[string]string{"api_key": "***"},
			wantKey: true,
		},
		{
			name:    "config fields, base_url only — passes through in the clear",
			info:    fieldsCfg,
			cfg:     map[string]string{"base_url": "http://isfdb-adapter:8080"},
			wantCfg: map[string]string{"base_url": "http://isfdb-adapter:8080"},
			wantKey: false,
		},
		{
			name:    "config fields, password field set — masked, hasAPIKey true",
			info:    fieldsCfg,
			cfg:     map[string]string{"api_key": "sk-secret"},
			wantCfg: map[string]string{"api_key": "***"},
			wantKey: true,
		},
		{
			name:    "config fields, both set — url clear, key masked",
			info:    fieldsCfg,
			cfg:     map[string]string{"base_url": "http://x:8080", "api_key": "sk-secret"},
			wantCfg: map[string]string{"base_url": "http://x:8080", "api_key": "***"},
			wantKey: true,
		},
		{
			name:    "config fields, empty string value is treated as unset",
			info:    fieldsCfg,
			cfg:     map[string]string{"base_url": "", "api_key": ""},
			wantCfg: map[string]string{},
			wantKey: false,
		},
		{
			name:    "config fields, unrelated cfg key ignored",
			info:    fieldsCfg,
			cfg:     map[string]string{"enabled": "true"},
			wantCfg: map[string]string{},
			wantKey: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotCfg, gotKey := maskProviderConfig(tc.info, tc.cfg)
			if !reflect.DeepEqual(gotCfg, tc.wantCfg) {
				t.Errorf("config = %#v, want %#v", gotCfg, tc.wantCfg)
			}
			if gotKey != tc.wantKey {
				t.Errorf("hasAPIKey = %v, want %v", gotKey, tc.wantKey)
			}
		})
	}
}
