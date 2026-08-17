// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/licences"
	"github.com/fireball1725/librarium-api/internal/version"
)

// ListComponents godoc
//
// @Summary     List server components
// @Description Every Go module linked into this API binary, with its version and SPDX licence identifier. Clients render this on their licences page so the notices cover the server as well as the client. A component whose licence is unknown is returned with an empty licence rather than omitted.
// @Tags        health
// @Produce     json
// @Security    BearerAuth
// @Success     200  {object}  object{version=string,components=[]object{name=string,version=string,licence=string}}
// @Router      /components [get]
func ListComponents(w http.ResponseWriter, r *http.Request) {
	components := licences.Components()
	if components == nil {
		// Not an error: a binary built outside module mode genuinely does not
		// know its dependencies. Say so with an empty list rather than a 500,
		// so the page renders what it does know.
		components = []licences.Component{}
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		// The server's own version, so a client showing several instances can
		// label whose component list it is holding.
		"version":    version.BuildVersion,
		"components": components,
	})
}
