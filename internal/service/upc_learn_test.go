// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/fireball1725/librarium-api/internal/providers"
	"github.com/google/uuid"
)

func TestLearnUPCPrefixChecksThePair(t *testing.T) {
	s := NewProviderService(providers.NewRegistry(), nil)
	// Hybrids' back cover and its own ISBN pair up.
	prefix, added, err := s.LearnUPCPrefix(context.Background(), "03714500799134906", "9780765349064", nil)
	if err != nil || prefix != "0765" || added {
		t.Errorf("Hybrids: %q %v %v", prefix, added, err)
	}
	// Hominids' back cover with Hybrids' ISBN doesn't.
	if _, _, err := s.LearnUPCPrefix(context.Background(), "03714500799134500", "9780765349064", nil); !errors.Is(err, ErrNotLearnable) {
		t.Errorf("mismatched pair: %v", err)
	}
}

type fakePrefixes struct{ learned []string }

func (f *fakePrefixes) ISBNPrefixes(context.Context, string) ([]string, error) { return f.learned, nil }
func (f *fakePrefixes) Learn(context.Context, string, string, string, *uuid.UUID) (bool, error) {
	return true, nil
}

type fakeISBNs map[string]string

func (f fakeISBNs) Info() providers.ProviderInfo {
	return providers.ProviderInfo{Name: "isbns", Capabilities: []string{providers.CapBookISBN}}
}
func (f fakeISBNs) Configure(map[string]string) {}
func (f fakeISBNs) Enabled() bool               { return true }
func (f fakeISBNs) LookupByISBN(_ context.Context, isbn string) (*providers.BookResult, error) {
	if title, ok := f[isbn]; ok {
		return &providers.BookResult{Provider: "isbns", Title: title}, nil
	}
	return nil, nil
}

// Tor has two ISBN prefixes. When the add-on is a real book under both, the
// lookup answers with the first and names the other instead of guessing.
func TestLookupUPCMergedNamesOtherBooks(t *testing.T) {
	candidates := providers.ISBNsFromUPCAddon("03714500799134500", "0812")
	reg := providers.NewRegistry()
	reg.Register(fakeISBNs{candidates[0]: "Hominids", candidates[1]: "Some Other Tor Book"})
	s := NewProviderService(reg, nil)
	s.SetUPCPrefixRepo(&fakePrefixes{learned: []string{"0812"}})

	merged, err := s.LookupUPCMerged(context.Background(), "03714500799134500")
	if err != nil {
		t.Fatal(err)
	}
	if merged.FromISBN != "9780765345004" || merged.Title == nil || merged.Title.Value != "Hominids" {
		t.Errorf("picked %s %+v", merged.FromISBN, merged.Title)
	}
	if len(merged.OtherISBNs) != 1 || merged.OtherISBNs[0].Title != "Some Other Tor Book" {
		t.Errorf("others = %+v", merged.OtherISBNs)
	}
}
