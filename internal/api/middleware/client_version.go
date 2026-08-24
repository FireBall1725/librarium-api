// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/version"
)

const (
	// ClientHeader names the calling first-party app. classifyClient in the
	// logger has read this since before anything sent it; the vocabulary is
	// web, ios and mcp.
	ClientHeader = "X-Librarium-Client"

	// ClientVersionHeader carries that app's own release string. It is a second
	// header rather than a suffix on ClientHeader so the logger keeps recording
	// exactly what it recorded before.
	ClientVersionHeader = "X-Librarium-Client-Version"

	// MinClientHeader repeats the requirement on the rejection, for clients
	// that surface a raw body and never parse the envelope.
	MinClientHeader = "X-Librarium-Min-Client-Version"

	// CodeClientTooOld lets a client tell this 4xx apart from an ordinary one
	// and show an update prompt instead of an error toast.
	CodeClientTooOld = "client_too_old"
)

// RequireClientVersion rejects first-party clients older than this release can
// serve, with 426 and a message naming the version required.
//
// It exists because the alternative is worse than an error: a client built
// against the previous response shapes does not fail cleanly, it renders empty
// panels and missing reading state, which reads as data loss rather than as a
// version mismatch.
//
// 426 rather than 401, deliberately. The web client intercepts 401 before
// reading the body, clears the session and reports "Session expired", so a
// 401 here would surface as a spurious logout.
//
// Must be installed inside RequireAuth: the exemption for personal access
// tokens needs the claims.
func RequireClientVersion(minimums map[string]string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		// No minimums means no release has broken the contract yet. Checking
		// per-request would be wasted work and, worse, would reject clients
		// that predate the headers for no reason at all.
		if len(minimums) == 0 {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			name := strings.ToLower(strings.TrimSpace(r.Header.Get(ClientHeader)))

			if name == "" {
				// Nothing identified itself. Two very different callers land
				// here, and they need opposite answers.
				//
				// A personal access token is a deliberate raw-API integration:
				// someone's script or a homelab cron. It has no UI to break and
				// was never promised a stable rendering, so let it through.
				//
				// An interactive session with no client header is a first-party
				// app built before the headers existed, which is exactly the
				// case this gate is for.
				if c := ClaimsFromContext(r.Context()); c != nil && c.FromToken {
					next.ServeHTTP(w, r)
					return
				}
				reject(w, "",
					"This server requires a newer Librarium app. Please update, then sign in again.")
				return
			}

			want, gated := minimums[name]
			if !gated {
				// A client this release has no opinion about. Not ours to gate.
				next.ServeHTTP(w, r)
				return
			}

			got := strings.TrimSpace(r.Header.Get(ClientVersionHeader))

			// Local builds report 0.0.0-dev. Gating them locks a developer out
			// of their own stack.
			if version.IsDev(got) {
				next.ServeHTTP(w, r)
				return
			}

			if version.Compare(got, want) < 0 {
				reject(w, want, fmt.Sprintf(
					"This server needs Librarium %s or newer for %s. %s Please update and try again.",
					want, name, runningPhrase(got)))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// runningPhrase describes what the client claimed to be, without asserting a
// version number when it did not send one or sent something unreadable.
func runningPhrase(got string) string {
	if got == "" {
		return "This copy did not report its version."
	}
	if !version.IsValid(got) {
		return fmt.Sprintf("This copy reported %q, which is not a version.", got)
	}
	return fmt.Sprintf("You are running %s.", got)
}

func reject(w http.ResponseWriter, want, msg string) {
	if want != "" {
		w.Header().Set(MinClientHeader, want)
	}
	respond.ErrorCode(w, http.StatusUpgradeRequired, CodeClientTooOld, msg)
}
