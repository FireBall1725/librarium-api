// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package version

import (
	"strconv"
	"strings"
)

// MinClients is the oldest version of each first-party client this release can
// serve. Keys match the X-Librarium-Client vocabulary that classifyClient in
// the logger middleware already understands.
//
// An empty map disables client gating entirely, which is correct until a
// release actually changes the response shapes. Populate it in the same commit
// that breaks the contract, never earlier: a minimum that is raised
// speculatively locks people out of a server that would have served them fine.
//
// This is deliberately a constant of the release rather than an instance
// setting. It states which shapes the server can produce, which is a fact about
// the binary, not a preference. Making it configurable would only let someone
// re-enable the broken experience the gate exists to prevent.
var MinClients = map[string]string{
	// "web": "26.9.0",
	// "ios": "26.9.0",
	// "mcp": "26.9.0",
}

// Compare orders two Librarium version strings, returning -1 if a is older
// than b, 0 if they are equivalent, and 1 if a is newer.
//
// The scheme is YY.M.revision, and M is not zero-padded, so a plain string
// comparison puts 26.10.0 before 26.9.0. Components are compared numerically
// instead.
//
// The pre-release suffix is deliberately ignored, so 26.9.0-rc.3 compares equal
// to 26.9.0. Semver would order the rc first, but an rc carries the shapes of
// the release it is a candidate for, and treating it as older would lock out
// exactly the testers a minimum is usually raised for. If one specific rc has
// to be excluded, raise the minimum to the next revision instead.
func Compare(a, b string) int {
	pa, aok := parse(a)
	pb, bok := parse(b)

	// Something unparseable sorts oldest, so a gate built on this fails closed.
	switch {
	case !aok && !bok:
		return 0
	case !aok:
		return -1
	case !bok:
		return 1
	}

	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// IsDev reports whether v is a local build rather than a published release.
// Every repo reports 0.0.0-dev when its version was not injected at link time,
// and the API and MCP append a local timestamp to it, so this matches on the
// numeric part rather than the whole string.
//
// Gating these out would lock a developer out of their own stack.
func IsDev(v string) bool {
	p, ok := parse(v)
	return ok && p == [3]int{0, 0, 0}
}

// IsValid reports whether v reads as a version at all. Compare deliberately
// treats unreadable input as oldest so a gate fails closed, which means it
// cannot be used to tell "very old" apart from "not a version"; this can.
func IsValid(v string) bool {
	_, ok := parse(v)
	return ok
}

// parse splits a version into its three numeric components, reporting false if
// the string is not a version at all. A missing component reads as zero, so
// "26.9" parses as 26.9.0.
func parse(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")

	// Drop any pre-release or build suffix, and any trailing local timestamp:
	// an uninjected build reports "0.0.0-dev 2026-08-24 07:19 EDT".
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return [3]int{}, false
	}

	var out [3]int
	for i, part := range strings.Split(v, ".") {
		if i > 2 {
			break
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}
