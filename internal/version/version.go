// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package version

import (
	"fmt"
	"time"
)

// Version is the current release. Format: YY.MM.revision (e.g. 26.4.0).
// A "-dev" suffix marks local unshipped builds. Set via ldflags in production builds.
var Version = "26.4.0"

// StartTime is when this process started.
var StartTime = time.Now()

// BuildVersion is the human-readable combined string used in API responses
// and the UI. Format: {version} {YYYY-MM-DD HH:MM ZONE}, e.g. 26.0.0-dev 2026-04-19 19:56 EDT.
// The timezone comes from the $TZ env var in the container (falls back to UTC).
// This lets you tell deployments apart at a glance without bumping the version.
var BuildVersion = fmt.Sprintf("%s %s", Version, StartTime.Local().Format("2006-01-02 15:04 MST"))
