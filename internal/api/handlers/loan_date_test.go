// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import "testing"

// Regression test for a bug found 2026-08-17: parseDate accepted only
// YYYY-MM-DD and returned nil on anything else, so a client that read a loan
// and wrote it back sent the RFC3339 timestamp the API had just given it, and
// the due date was silently cleared. Marking a loan returned erased when it
// had been due.
func TestParseLoanDate(t *testing.T) {
	t.Run("accepts the documented format", func(t *testing.T) {
		got, err := parseLoanDate("2026-07-06")
		if err != nil || got == nil {
			t.Fatalf("want a date, got %v err %v", got, err)
		}
		if got.Format("2006-01-02") != "2026-07-06" {
			t.Fatalf("wrong date: %s", got)
		}
	})

	t.Run("accepts what the API itself emits", func(t *testing.T) {
		// This is the exact shape a loan's due_date comes back as.
		got, err := parseLoanDate("2026-07-06T00:00:00Z")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("round-tripping the API's own value must not clear the field")
		}
		if got.Format("2006-01-02") != "2026-07-06" {
			t.Fatalf("wrong date: %s", got)
		}
	})

	t.Run("empty means no date", func(t *testing.T) {
		got, err := parseLoanDate("")
		if err != nil || got != nil {
			t.Fatalf("want nil with no error, got %v err %v", got, err)
		}
	})

	t.Run("nonsense is an error, not a silent clear", func(t *testing.T) {
		// The whole point. Swallowing this is what let a bad value look like
		// an intentional "remove the due date".
		if _, err := parseLoanDate("last thursday"); err == nil {
			t.Fatal("want an error so the caller hears about it")
		}
	})
}
