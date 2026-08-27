// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The docs page has to work wherever the API is mounted.
//
// Nothing else in this service cares: every path it hands a client is
// root-relative and the client resolves it against its own base, so an API
// behind a reverse proxy at /librarium-api needs no configuration. The docs
// page was the exception, because it is the one thing here a browser loads
// directly, and a browser resolves an absolute path against the origin rather
// than against wherever the page lives.
func TestDocsReferencesTheSpecRelatively(t *testing.T) {
	rec := httptest.NewRecorder()
	ServeScalarUI(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `data-url="openapi.json"`) {
		t.Errorf("docs page does not reference the spec relatively")
	}
	// The shape that breaks: from /librarium-api/api/docs a browser asks the
	// host root for /api/openapi.json, which is the one place it is not.
	if strings.Contains(body, `data-url="/api/openapi.json"`) {
		t.Errorf("docs page references the spec by an absolute path")
	}
}
