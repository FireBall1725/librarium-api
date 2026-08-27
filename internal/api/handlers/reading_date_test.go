// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"testing"
	"time"
)

// Regression test for a bug found 2026-08-27 while checking librarium-api#65:
// every reading date came back a day early on any server not running in UTC.
//
// `date_started` and `date_finished` are dates wearing a timestamp. The client
// sends YYYY-MM-DD, the handler parses it to midnight UTC, and the column is a
// `timestamptz` because the same table carries real instants for progress. The
// render then formatted it in whatever zone the connection returned, so
// midnight UTC read west of Greenwich is the previous day: a book finished on
// 1 January came back finished on 31 December.
//
// The deployment compose files mount /etc/localtime, so this is every
// self-hosted instance outside UTC — and none of the CI that tests it, which
// is why it survived.
func TestReadingDateIsTheDayItWasWrittenAs(t *testing.T) {
	day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("in UTC", func(t *testing.T) {
		if got := readingDate(&day); got != "2026-01-01" {
			t.Errorf("got %v, want 2026-01-01", got)
		}
	})

	t.Run("read back west of Greenwich", func(t *testing.T) {
		// What pgx hands back when the server runs in, say, Toronto: the same
		// instant, carrying a zone four hours behind. Formatting it there is
		// what produced 2025-12-31.
		toronto := time.FixedZone("EST", -5*60*60)
		shifted := day.In(toronto)
		if got := readingDate(&shifted); got != "2026-01-01" {
			t.Errorf("got %v, want 2026-01-01", got)
		}
	})

	t.Run("read back east of Greenwich", func(t *testing.T) {
		// The other direction rounds the wrong way just as easily.
		sydney := time.FixedZone("AEDT", 11*60*60)
		shifted := day.In(sydney)
		if got := readingDate(&shifted); got != "2026-01-01" {
			t.Errorf("got %v, want 2026-01-01", got)
		}
	})

	t.Run("no date stays absent", func(t *testing.T) {
		// nil rather than an empty string: the client tells "never started"
		// apart from "started, date unknown" by the null.
		if got := readingDate(nil); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}
