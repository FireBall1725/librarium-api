// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package service

import "testing"

func TestProviderListed(t *testing.T) {
	cases := []struct {
		name    string
		cfg     map[string]string
		enabled bool
		want    bool
	}{
		{"on before the flag existed", nil, true, true},
		{"off and never added", nil, false, false},
		{"added, then switched off", map[string]string{"listed": "true"}, false, true},
		{"removed", map[string]string{"listed": "false"}, false, false},
	}
	for _, c := range cases {
		if got := providerListed(c.cfg, c.enabled); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
