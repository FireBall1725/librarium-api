// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package middleware

import "net/http"

// SecurityHeaders sets a conservative set of HTTP security response headers
// on every response. Applied globally before routing so it covers both API
// JSON responses and the docs / OpenAPI surfaces.
//
// Notes on header choices:
//
//   - HSTS forces TLS for clients that have ever seen an HTTPS response from
//     this origin. We set 1y with includeSubDomains (no preload — operators
//     should opt into hstspreload.org consciously). This is a no-op when the
//     request arrives over plaintext, which is desired during local dev.
//   - X-Content-Type-Options blocks MIME sniffing — important for the JSON
//     API surface (responses are always application/json) and the cover
//     proxy (which sets the right Content-Type but should not be overridden).
//   - X-Frame-Options + frame-ancestors block clickjacking on the docs UI;
//     the API itself never renders HTML so the browser-facing concern is the
//     scalar UI at /api/docs.
//   - The CSP is intentionally tight — `default-src 'self'` plus the inline
//     allowances Scalar UI needs to render. If we ever serve a richer
//     browser surface from this binary the CSP can be relaxed at that route.
//   - Referrer-Policy keeps the path/query of any links out of cross-origin
//     Referer headers; useful for any future deep-link surfaces.
//   - Permissions-Policy turns off browser features we never legitimately
//     use from API responses.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		// Scalar UI (the OpenAPI viewer at /api/docs) loads its bundle from
		// jsdelivr and renders inline styles, so loosen CSP only for that
		// route. Everywhere else we ship a tight default-src 'self'.
		if r.URL.Path == "/api/docs" {
			h.Set("Content-Security-Policy",
				"default-src 'self'; "+
					"script-src 'self' https://cdn.jsdelivr.net 'unsafe-inline'; "+
					"style-src 'self' https://cdn.jsdelivr.net 'unsafe-inline'; "+
					"img-src 'self' data: https:; "+
					"font-src 'self' data: https://cdn.jsdelivr.net; "+
					"connect-src 'self'; "+
					"frame-ancestors 'none'")
		} else {
			h.Set("Content-Security-Policy",
				"default-src 'none'; frame-ancestors 'none'")
		}
		next.ServeHTTP(w, r)
	})
}
