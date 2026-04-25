// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"net/http"

	"github.com/swaggo/swag"
	_ "github.com/fireball1725/librarium-api/docs"
)

// ServeOpenAPISpec serves the generated OpenAPI JSON spec.
func ServeOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	doc, err := swag.ReadDoc()
	if err != nil {
		http.Error(w, "failed to read spec", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write([]byte(doc))
}

// ServeScalarUI serves the Scalar API reference UI.
//
// Themed with Scalar's `purple` preset, which matches the Librarium
// marketing-site brand: slate backgrounds (`#15171c` family) with an
// indigo accent (`#5469d4` — the closest preset match to the
// `#6366f1` we use elsewhere). The accent CSS variable is overridden
// to the exact brand colour so docs, web UI, and the marketing site
// land on the same indigo.
func ServeScalarUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Librarium API Reference</title>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <style>
      body { margin: 0; }
      /* Pin the Scalar accent to Librarium's exact brand indigo so
         the docs, web UI, and marketing site share one identity. The
         purple preset's default accent (#5469d4) is a hair off the
         #6366f1 we use elsewhere. */
      :root {
        --scalar-color-accent: #6366f1;
        --scalar-color-accent-light: #818cf81f;
      }
    </style>
  </head>
  <body>
    <script
      id="api-reference"
      data-url="/api/openapi.json"
      data-configuration='{"theme":"purple"}'></script>
    <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
  </body>
</html>`))
}
