// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package service

import (
	"context"
	"testing"

	"github.com/fireball1725/librarium-api/internal/providers"
)

type fakeUPC struct{ asked string }

func (f *fakeUPC) Info() providers.ProviderInfo {
	return providers.ProviderInfo{Name: "upc", Capabilities: []string{providers.CapBookUPC}}
}
func (f *fakeUPC) Configure(map[string]string) {}
func (f *fakeUPC) Enabled() bool               { return true }
func (f *fakeUPC) LookupByUPC(_ context.Context, code string) (*providers.BookResult, error) {
	f.asked = code
	return &providers.BookResult{Title: "Ender's Game"}, nil
}

type fakeSeries struct{ asked string }

func (f *fakeSeries) Info() providers.ProviderInfo {
	return providers.ProviderInfo{Name: "series", Capabilities: []string{providers.CapSeriesName}}
}
func (f *fakeSeries) Configure(map[string]string) {}
func (f *fakeSeries) Enabled() bool               { return true }
func (f *fakeSeries) SearchSeries(_ context.Context, q string) ([]providers.SeriesResult, error) {
	f.asked = q
	return []providers.SeriesResult{{Name: "One Piece"}}, nil
}

// The Test button used to try an ISBN on every provider, so UPCitemdb and
// MangaDex always failed with "does not support ISBN lookup".
func TestTestProviderUsesWhatTheProviderCanDo(t *testing.T) {
	upc, series := &fakeUPC{}, &fakeSeries{}
	reg := providers.NewRegistry()
	reg.Register(upc)
	reg.Register(series)
	s := NewProviderService(reg, nil)

	if got, err := s.TestProvider(context.Background(), "upc"); err != nil || got != "Ender's Game" || upc.asked != defaultTestUPC {
		t.Errorf("upc: %q, %v, asked %q", got, err, upc.asked)
	}
	if got, err := s.TestProvider(context.Background(), "series"); err != nil || got != "One Piece" || series.asked != defaultTestSeries {
		t.Errorf("series: %q, %v, asked %q", got, err, series.asked)
	}
}
