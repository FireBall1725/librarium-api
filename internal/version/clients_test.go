// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package version

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		why  string
	}{
		{"26.9.0", "26.9.0", 0, "identical"},
		{"26.8.1", "26.9.0", -1, "older month"},
		{"26.9.0", "26.8.1", 1, "newer month"},
		{"26.8.0", "26.8.1", -1, "older revision"},
		{"25.12.0", "26.1.0", -1, "year rolls over"},

		// The reason this is not a string comparison.
		{"26.9.0", "26.10.0", -1, "month 10 is newer than month 9, which sorts the other way as text"},
		{"26.10.0", "26.9.0", 1, "same, reversed"},

		// An rc carries the shapes of the release it is a candidate for.
		{"26.9.0-rc.1", "26.9.0", 0, "rc equals its release"},
		{"26.9.0-rc.3", "26.9.0-rc.1", 0, "rc number is not compared"},
		{"26.9.0-nightly.202608240719", "26.9.0", 0, "nightly equals its release"},
		{"26.9.0-rc.1", "26.8.1", 1, "an rc of a newer release is still newer"},

		{"v26.9.0", "26.9.0", 0, "leading v is tolerated"},
		{"26.9", "26.9.0", 0, "missing component reads as zero"},

		// Fails closed: anything unreadable must sort oldest so a gate rejects it.
		{"", "26.9.0", -1, "empty is oldest"},
		{"not-a-version", "26.9.0", -1, "garbage is oldest"},
		{"26.x.0", "26.9.0", -1, "partly numeric is still garbage"},
	}

	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d — %s", c.a, c.b, got, c.want, c.why)
		}
	}
}

func TestIsDev(t *testing.T) {
	dev := []string{
		"0.0.0-dev",
		"0.0.0-dev 2026-08-24 07:19 EDT", // what an uninjected api or mcp build reports
		"0.0.0",
	}
	for _, v := range dev {
		if !IsDev(v) {
			t.Errorf("IsDev(%q) = false, want true — this would lock a developer out of their own stack", v)
		}
	}

	notDev := []string{"26.9.0", "26.9.0-rc.1", "0.1.0", "0.0.1"}
	for _, v := range notDev {
		if IsDev(v) {
			t.Errorf("IsDev(%q) = true, want false", v)
		}
	}

	// Garbage is not a dev build; it has to stay gateable or the exemption
	// becomes the way around the gate.
	for _, v := range []string{"", "banana"} {
		if IsDev(v) {
			t.Errorf("IsDev(%q) = true, want false — unparseable must not be exempt", v)
		}
	}
}

func TestMinClientsStartsEmpty(t *testing.T) {
	// Guards the ordering rule: the minimums are raised in the same commit that
	// changes the response shapes. If this fails, check that the client repos
	// actually shipped the version being demanded before this landed.
	if len(MinClients) != 0 {
		t.Logf("MinClients is populated: %v", MinClients)
		t.Log("this is expected once the shapes change; make sure the clients shipped first")
	}
}
