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
// @Description Returns the API health status and current version
// @Tags        health
// @Produce     json
// @Success     200  {object}  object{status=string,version=string,started_at=string}
// @Router      /health [get]
func Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"status":     "ok",
		"version":    version.BuildVersion,
		"started_at": version.StartTime.UTC().Format(time.RFC3339),
	})
}
