// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package providers

import "strings"

// nameSuffixes follow a name after a comma without being a name themselves:
// "Martin Luther King, Jr." is one author, not two. Compared lowercased with
// the dots taken out.
var nameSuffixes = map[string]bool{
	"jr": true, "sr": true, "ii": true, "iii": true, "iv": true, "v": true,
	"phd": true, "md": true, "esq": true, "dds": true, "obe": true, "mbe": true, "cbe": true, "kbe": true,
}

func isNameSuffix(part string) bool {
	return nameSuffixes[strings.ToLower(strings.ReplaceAll(strings.TrimSpace(part), ".", ""))]
}

// SplitAuthorNames splits a comma-separated list of names, keeping a suffix
// with the name before it. Only for text that was already joined; a
// provider's own list should go through JoinNameSuffixes instead.
func SplitAuthorNames(s string) []string {
	return JoinNameSuffixes(strings.Split(s, ","))
}

// JoinNameSuffixes trims a list of names, drops empty ones, and folds a
// suffix that arrived as its own entry back onto the name before it.
func JoinNameSuffixes(parts []string) []string {
	var names []string
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if len(names) > 0 && isNameSuffix(p) {
			names[len(names)-1] += ", " + p
			continue
		}
		names = append(names, p)
	}
	return names
}

// AuthorNames is the author list of a merged field: Values when the server
// that built it kept them, else Value split with suffixes kept together.
func (f *FieldResult) AuthorNames() []string {
	if f == nil {
		return nil
	}
	if len(f.Values) > 0 {
		return f.Values
	}
	return SplitAuthorNames(f.Value)
}
