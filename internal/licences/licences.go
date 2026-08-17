// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

// Package licences reports what the API server is built from.
//
// The AGPL obliges us to carry each dependency's notice, and a self-hosted
// application should be able to answer "what is running on my machine" without
// its operator reading a lockfile. Both wants the same list, so the API serves
// it and every client renders it.
//
// The module list is NOT written down here. It comes from the binary's own
// build info, so it describes the server that answers the request rather than
// whatever the source tree looked like when someone last remembered to update a
// page. A client talking to three Librarium instances on three versions gets
// three honest answers. Only the SPDX identifier is curated, because a Go
// module does not declare its licence anywhere a program can read at runtime.
//
// Adding a dependency therefore cannot drop a component from this list; it can
// only leave the new module's licence blank, and the package test fails on that.
// The list stays complete on its own and CI catches the one part that needs a
// human.
//
// The test reads go.mod rather than build info, because a *test* binary reports
// zero dependencies from debug.ReadBuildInfo — the call succeeds and Deps is
// simply empty, so a test written against it passes by iterating over nothing
// while a missing licence sails through. go.mod is a superset of what gets
// linked, which errs the safe way: a row for something we do not ship costs
// nothing, a shipped module with no notice is the failure that matters.
package licences

import (
	"runtime/debug"
	"sort"
	"strings"
)

// Component is one module linked into this binary.
type Component struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Licence is the SPDX identifier, or "" when the module is missing from
	// spdxByModule. Empty is deliberately serialised rather than dropped: a
	// component with an unknown licence is exactly the one worth showing.
	Licence string `json:"licence"`
}

// spdxByModule maps a module path to its SPDX identifier.
//
// Read from each module's own LICENSE file in the module cache. README.md in
// this package has the detector script and the two traps worth knowing about;
// the package test tells you when a refresh is due.
var spdxByModule = map[string]string{
	"github.com/anthropics/anthropic-sdk-go": "MIT",
	"github.com/aymanbagabas/go-osc52/v2":    "MIT",
	"github.com/bahlo/generic-list-go":       "BSD-3-Clause",
	"github.com/buger/jsonparser":            "MIT",
	"github.com/charmbracelet/bubbletea":     "MIT",
	"github.com/charmbracelet/colorprofile":  "MIT",
	"github.com/charmbracelet/lipgloss":      "MIT",
	"github.com/charmbracelet/x/ansi":        "MIT",
	"github.com/charmbracelet/x/cellbuf":     "MIT",
	"github.com/charmbracelet/x/term":        "MIT",
	"github.com/davecgh/go-spew":             "ISC",
	"github.com/erikgeiser/coninput":         "MIT",
	"github.com/go-openapi/jsonpointer":      "Apache-2.0",
	"github.com/go-openapi/jsonreference":    "Apache-2.0",
	"github.com/go-openapi/spec":             "Apache-2.0",
	"github.com/go-openapi/swag":             "Apache-2.0",
	"github.com/golang-jwt/jwt/v5":           "MIT",
	"github.com/golang-migrate/migrate/v4":   "MIT",
	"github.com/google/uuid":                 "BSD-3-Clause",
	"github.com/invopop/jsonschema":          "MIT",
	"github.com/jackc/pgerrcode":             "MIT",
	"github.com/jackc/pgpassfile":            "MIT",
	"github.com/jackc/pgservicefile":         "MIT",
	"github.com/jackc/pgx/v5":                "MIT",
	"github.com/jackc/puddle/v2":             "MIT",
	"github.com/josharian/intern":            "MIT",
	"github.com/KyleBanks/depth":             "MIT",
	"github.com/lucasb-eyer/go-colorful":     "MIT",
	"github.com/mailru/easyjson":             "MIT",
	"github.com/mattn/go-isatty":             "MIT",
	// No LICENSE file in the module; its README states MIT.
	"github.com/mattn/go-localereader":                         "MIT",
	"github.com/mattn/go-runewidth":                            "MIT",
	"github.com/muesli/ansi":                                   "MIT",
	"github.com/muesli/cancelreader":                           "MIT",
	"github.com/muesli/termenv":                                "MIT",
	"github.com/pb33f/ordered-map/v2":                          "Apache-2.0",
	"github.com/pmezard/go-difflib":                            "BSD-2-Clause",
	"github.com/PuerkitoBio/purell":                            "BSD-3-Clause",
	"github.com/PuerkitoBio/urlesc":                            "BSD-3-Clause",
	"github.com/riverqueue/river":                              "MPL-2.0",
	"github.com/riverqueue/river/riverdriver":                  "MPL-2.0",
	"github.com/riverqueue/river/riverdriver/riverpgxv5":       "MPL-2.0",
	"github.com/riverqueue/river/rivershared":                  "MPL-2.0",
	"github.com/riverqueue/river/rivertype":                    "MPL-2.0",
	"github.com/rivo/uniseg":                                   "MIT",
	"github.com/robfig/cron/v3":                                "MIT",
	"github.com/standard-webhooks/standard-webhooks/libraries": "MIT",
	"github.com/stretchr/testify":                              "MIT",
	"github.com/swaggo/swag":                                   "MIT",
	"github.com/tidwall/gjson":                                 "MIT",
	"github.com/tidwall/match":                                 "MIT",
	"github.com/tidwall/pretty":                                "MIT",
	"github.com/tidwall/sjson":                                 "MIT",
	"github.com/xo/terminfo":                                   "MIT",
	"go.uber.org/goleak":                                       "MIT",
	"go.yaml.in/yaml/v4":                                       "Apache-2.0",
	"golang.org/x/crypto":                                      "BSD-3-Clause",
	"golang.org/x/mod":                                         "BSD-3-Clause",
	"golang.org/x/net":                                         "BSD-3-Clause",
	"golang.org/x/sync":                                        "BSD-3-Clause",
	"golang.org/x/sys":                                         "BSD-3-Clause",
	"golang.org/x/text":                                        "BSD-3-Clause",
	"golang.org/x/tools":                                       "BSD-3-Clause",
	"gopkg.in/yaml.v2":                                         "Apache-2.0",
	"gopkg.in/yaml.v3":                                         "Apache-2.0",
}

// Components returns every module linked into this binary, sorted by name.
//
// Returns nil when build info is unavailable, which happens for a binary built
// outside module mode. Callers report that as an empty list rather than an
// error: not knowing the dependency set is not a server fault, and a licences
// page that renders nothing is more honest than one that renders a 500.
func Components() []Component {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	return componentsFrom(info.Deps)
}

// componentsFrom is the whole of Components' logic, split out so it can be
// tested: a test binary's own build info carries no dependencies, so a test
// calling Components() would only ever see an empty list.
func componentsFrom(deps []*debug.Module) []Component {
	out := make([]Component, 0, len(deps))
	for _, d := range deps {
		// A replaced module reports its replacement's path and version, which
		// is what is actually compiled in and therefore what carries the notice.
		m := d
		if m.Replace != nil {
			m = m.Replace
		}
		out = append(out, Component{
			Name:    m.Path,
			Version: m.Version,
			Licence: spdxByModule[m.Path],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}
