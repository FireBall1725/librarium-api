// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"strings"
	"unicode"
)

// Suffixes kept attached to the given-name portion when inverting a name.
// "Robert Downey Jr." → "Downey, Robert Jr.", not "Jr., Robert Downey".
var nameSuffixes = map[string]struct{}{
	"jr":    {},
	"jr.":   {},
	"sr":    {},
	"sr.":   {},
	"ii":    {},
	"iii":   {},
	"iv":    {},
	"v":     {},
	"esq":   {},
	"esq.":  {},
	"phd":   {},
	"ph.d":  {},
	"ph.d.": {},
	"md":    {},
	"m.d.":  {},
}

// Lowercase surname particles that stay glued to the surname when inverting.
// "Ludwig van Beethoven" → "van Beethoven, Ludwig".
var surnameParticles = map[string]struct{}{
	"van":    {},
	"von":    {},
	"de":     {},
	"del":    {},
	"della":  {},
	"der":    {},
	"di":     {},
	"du":     {},
	"da":     {},
	"la":     {},
	"le":     {},
	"lo":     {},
	"ten":    {},
	"ter":    {},
	"bin":    {},
	"ibn":    {},
	"al":     {},
	"el":     {},
	"mac":    {},
	"mc":     {},
	"st":     {},
	"st.":    {},
	"saint":  {},
	"sainte": {},
}

// DeriveSortName produces a library-style "Last, First" sort key from a
// display name. If the input already contains a comma it is returned as-is
// (the caller has already supplied a sort form). For corporate entities
// (publishers, studios) pass is_corporate=true to the caller instead; this
// function makes no judgement about that.
//
// Rules, in order:
//  1. Non-empty input only — empty returns empty.
//  2. Already has a comma → trim and return as-is.
//  3. Single token → return as-is (mononyms, handles).
//  4. Strip trailing suffixes (Jr., Sr., II…) before picking the surname,
//     then reattach them after the given name.
//  5. Gather trailing lowercase particles (van, von, de…) onto the surname.
//  6. Format "Surname, Given Names [Suffix]".
func DeriveSortName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.Contains(name, ",") {
		return name
	}
	// Any parenthesis in a name is almost always an import artifact
	// (e.g. "(Artist)", "(Letterer (comics))"). Inverting such a string
	// produces garbage like "(comics)), Anthony Quintessenza (Letterer".
	// Fall back to the display name as the sort key — a pre-import cleanup
	// should strip the suffix; this keeps us from fabricating nonsense in
	// the meantime.
	if strings.ContainsAny(name, "()[]") {
		return name
	}

	tokens := strings.Fields(name)
	if len(tokens) == 1 {
		return tokens[0]
	}

	// Pull trailing suffix tokens off the end.
	var suffixes []string
	for len(tokens) > 1 {
		last := tokens[len(tokens)-1]
		key := strings.ToLower(strings.TrimRight(last, ","))
		if _, ok := nameSuffixes[key]; !ok {
			break
		}
		suffixes = append([]string{last}, suffixes...)
		tokens = tokens[:len(tokens)-1]
	}

	if len(tokens) == 1 {
		// Just a mononym plus suffix(es): "Cher Jr." → "Cher Jr."
		if len(suffixes) > 0 {
			return tokens[0] + " " + strings.Join(suffixes, " ")
		}
		return tokens[0]
	}

	// Collect trailing particles onto the surname. Walk back while the token
	// immediately before the current surname is a lowercase particle.
	surnameStart := len(tokens) - 1
	for surnameStart > 1 {
		prev := tokens[surnameStart-1]
		if !isParticle(prev) {
			break
		}
		surnameStart--
	}

	given := strings.Join(tokens[:surnameStart], " ")
	surname := strings.Join(tokens[surnameStart:], " ")

	if len(suffixes) > 0 {
		given = given + " " + strings.Join(suffixes, " ")
	}
	return surname + ", " + given
}

func isParticle(tok string) bool {
	if tok == "" {
		return false
	}
	// Must be entirely lowercase to count — "De" at the start of a name
	// (e.g. "De Niro") is treated as part of the surname by convention.
	for _, r := range tok {
		if unicode.IsLetter(r) && !unicode.IsLower(r) {
			return false
		}
	}
	key := strings.ToLower(strings.TrimRight(tok, "."))
	_, ok := surnameParticles[key]
	return ok
}
