// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package service

import (
	"testing"
	"time"

	"github.com/fireball1725/librarium-api/internal/providers"
)

func TestRecentAnswers(t *testing.T) {
	s := NewProviderService(providers.NewRegistry(), nil)
	answers := []*providers.BookResult{{Provider: "open_library", Title: "Dune"}}

	s.rememberAnswers("0441172717", answers)
	if got := s.RecentAnswers("9780441172719"); len(got) != 1 {
		t.Fatalf("an ISBN-10 lookup should be found by its ISBN-13, got %v", got)
	}
	if got := s.RecentAnswers("9780000000002"); got != nil {
		t.Errorf("unknown code: got %v", got)
	}

	s.rememberAnswers("9780553103540", nil)
	if got := s.RecentAnswers("9780553103540"); got != nil {
		t.Errorf("a lookup nobody answered stores nothing, got %v", got)
	}

	// Stale answers aren't handed out.
	s.recent[providers.BarcodeKey("0441172717")] = recentAnswers{results: answers, at: time.Now().Add(-2 * recentAnswersTTL)}
	if got := s.RecentAnswers("0441172717"); got != nil {
		t.Errorf("expired answers returned: %v", got)
	}
}

func TestRecentAnswersStaysBounded(t *testing.T) {
	s := NewProviderService(providers.NewRegistry(), nil)
	answers := []*providers.BookResult{{Provider: "p"}}
	for i := 0; i < recentAnswersMax+50; i++ {
		s.rememberAnswers(string(rune('a'+i%26))+time.Now().Format("150405.000000000")+string(rune(i)), answers)
	}
	if len(s.recent) > recentAnswersMax {
		t.Errorf("cache grew to %d, cap is %d", len(s.recent), recentAnswersMax)
	}
}
