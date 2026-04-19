// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package version

import (
	"fmt"
	"time"
)

// Version is the current release, set at link time via ldflags for release
// builds (e.g. "26.4.1"). When empty (local dev builds), it's auto-computed
// from the current date as "{YY}.{M}.DEV" during package init. Format:
// YY.MM.revision, not zero-padded.
var Version = ""

// StartTime is when this process started.
var StartTime = time.Now()

// BuildVersion is the human-readable combined string used in API responses
// and the UI. Release builds: "{YY.MM.rev}". Dev builds: the auto-computed
// version plus a local timestamp so deployments are distinguishable at a
// glance, e.g. "26.4.DEV 2026-04-19 19:56 EDT".
var BuildVersion = buildVersion()

func buildVersion() string {
	if Version == "" {
		Version = fmt.Sprintf("%d.%d.DEV", StartTime.Year()%100, int(StartTime.Month()))
		return fmt.Sprintf("%s %s", Version, StartTime.Local().Format("2006-01-02 15:04 MST"))
	}
	return Version
}
