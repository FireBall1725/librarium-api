// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/fireball1725/librarium-api/internal/version"
)

// Health godoc
//
// @Summary     Health check
// @Description Returns the API health status, current version, and the oldest client version this server can serve.
// @Tags        health
// @Produce     json
// @Success     200  {object}  object{status=string,version=string,started_at=string,min_clients=object}
// @Router      /health [get]
func Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// min_clients is published here because /health is unauthenticated and the
	// web client already polls it at boot. That lets a client say "this server
	// needs a newer app" before any request has failed, rather than discovering
	// it from a rejection partway through a session. An empty object means no
	// client is being gated.
	json.NewEncoder(w).Encode(map[string]any{
		"status":      "ok",
		"version":     version.BuildVersion,
		"started_at":  version.StartTime.UTC().Format(time.RFC3339),
		"min_clients": version.MinClients,
	})
}
