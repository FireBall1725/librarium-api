// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

// Package search implements the Librarium query language parser.
//
// The query language supports:
//
//	bleach                  → title contains "bleach"
//	"exact phrase"          → title phrase-matches "exact phrase"
//	/regex/                 → title ~* regex
//	NOT term                → negate next condition
//	type:Manga              → media-type equals "Manga"
//	tag:romance             → tag equals "romance"
//	genre:Fantasy           → genre equals "Fantasy"
//	contributor:name        → contributor name contains "name"
//	author:name             → alias for contributor
//	title:word              → title contains "word"
//	isbn:9780...            → isbn equals value
//	letter:b                → first-letter filter
//	series:name             → book belongs to series "name"
//	shelf:name              → book is on shelf "name"
//	publisher:name          → primary edition publisher equals "name"
//	language:en             → primary edition language equals "en"
//	has:cover               → has a cover image
//	(cond OR cond)          → OR group
//	term1 term2             → AND group (default)
//
// Multiple top-level groups are ANDed together.
package search

import (
	"strings"
	"unicode"

	"github.com/fireball1725/librarium-api/internal/repository"
)

// Parse turns a raw query string into grouped filter conditions ready for
// repository.ListBooksOpts.Groups.
func Parse(raw string) []repository.ConditionGroup {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	tokens := tokenize(raw)
	if len(tokens) == 0 {
		return nil
	}

	groups, _ := parseTokens(tokens, false)
	if len(groups) == 0 {
		return nil
	}
	return groups
}

// ─── Tokenizer ────────────────────────────────────────────────────────────────

// tokenize splits the query into tokens, treating:
//   - quoted strings ("...") as a single token
//   - /regex/ as a single token
//   - field:value as a single token
//   - parentheses as individual tokens
func tokenize(q string) []string {
	var tokens []string
	runes := []rune(q)
	i := 0
	for i < len(runes) {
		// Skip whitespace
		for i < len(runes) && unicode.IsSpace(runes[i]) {
			i++
		}
		if i >= len(runes) {
			break
		}

		switch runes[i] {
		case '(':
			tokens = append(tokens, "(")
			i++
		case ')':
			tokens = append(tokens, ")")
			i++
		case '"':
			// Quoted string
			i++ // skip opening quote
			start := i
			for i < len(runes) && runes[i] != '"' {
				i++
			}
			tokens = append(tokens, `"`+string(runes[start:i])+`"`)
			if i < len(runes) {
				i++ // skip closing quote
			}
		case '/':
			// Regex /pattern/
			i++
			start := i
			for i < len(runes) && runes[i] != '/' {
				i++
			}
			tokens = append(tokens, `/`+string(runes[start:i])+`/`)
			if i < len(runes) {
				i++ // skip closing /
			}
		default:
			// Word (possibly field:value or field:"quoted value")
			start := i
			// Peek ahead: if this token looks like field:"...", consume through the closing quote
			// so that multi-word values like contributor:"Tite Kubo" become one token.
			j := i
			for j < len(runes) && runes[j] != ':' && !unicode.IsSpace(runes[j]) && runes[j] != '(' && runes[j] != ')' {
				j++
			}
			if j < len(runes) && runes[j] == ':' && j+1 < len(runes) && runes[j+1] == '"' {
				j += 2 // skip ':' and opening '"'
				for j < len(runes) && runes[j] != '"' {
					j++
				}
				if j < len(runes) {
					j++ // skip closing '"'
				}
				tokens = append(tokens, string(runes[start:j]))
				i = j
			} else {
				for i < len(runes) && !unicode.IsSpace(runes[i]) && runes[i] != '(' && runes[i] != ')' {
					i++
				}
				tokens = append(tokens, string(runes[start:i]))
			}
		}
	}
	return tokens
}

// ─── Parser ───────────────────────────────────────────────────────────────────

// parseTokens processes a token slice. stopAtParen=true when inside (...).
// Returns the groups produced and the remaining token count consumed.
func parseTokens(tokens []string, stopAtParen bool) ([]repository.ConditionGroup, int) {
	var (
		outerGroups []repository.ConditionGroup // groups completed so far
		current     []repository.FilterCondition
		currentMode = "AND"
		negate      = false
	)

	flush := func() {
		if len(current) > 0 {
			outerGroups = append(outerGroups, repository.ConditionGroup{
				Mode:       currentMode,
				Conditions: current,
			})
			current = nil
			currentMode = "AND"
		}
	}

	i := 0
	for i < len(tokens) {
		tok := tokens[i]

		switch {
		case tok == ")":
			if stopAtParen {
				i++
				goto done
			}
			i++

		case tok == "(":
			// Parse inner group
			subGroups, consumed := parseTokens(tokens[i+1:], true)
			i += consumed + 1 // +1 for the "("
			// Flatten inner groups into a single condition group
			for _, g := range subGroups {
				// If top-level current is non-empty, flush it first
				if len(g.Conditions) > 0 {
					// Each paren group becomes its own top-level group
					if negate {
						// Negate all conditions in the group
						for ci := range g.Conditions {
							g.Conditions[ci].Op = negateOp(g.Conditions[ci].Op)
						}
						negate = false
					}
					outerGroups = append(outerGroups, g)
				}
			}

		case strings.EqualFold(tok, "NOT"):
			negate = true
			i++

		case strings.EqualFold(tok, "AND"):
			// explicit AND — flush current group and start new one with AND mode
			flush()
			currentMode = "AND"
			i++

		case strings.EqualFold(tok, "OR"):
			// OR changes the mode for the current group
			currentMode = "OR"
			i++

		default:
			cond := parseSingleToken(tok)
			if cond != nil {
				if negate {
					cond.Op = negateOp(cond.Op)
					negate = false
				}
				current = append(current, *cond)
			}
			i++
		}
	}

done:
	flush()
	return outerGroups, i
}

// parseSingleToken converts one token into a FilterCondition.
func parseSingleToken(tok string) *repository.FilterCondition {
	// Quoted phrase
	if strings.HasPrefix(tok, `"`) && strings.HasSuffix(tok, `"`) && len(tok) >= 2 {
		val := tok[1 : len(tok)-1]
		if val == "" {
			return nil
		}
		return &repository.FilterCondition{Field: "title", Op: "phrase", Value: val}
	}

	// Regex /pattern/
	if strings.HasPrefix(tok, "/") && strings.HasSuffix(tok, "/") && len(tok) >= 2 {
		val := tok[1 : len(tok)-1]
		if val == "" {
			return nil
		}
		return &repository.FilterCondition{Field: "title", Op: "regex", Value: val}
	}

	// field:value
	if idx := strings.IndexByte(tok, ':'); idx > 0 && idx < len(tok)-1 {
		field := strings.ToLower(tok[:idx])
		val := tok[idx+1:]
		// Strip outer quotes from value if present
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		return fieldCondition(field, val)
	}

	// Plain term → title contains
	if tok == "" {
		return nil
	}
	return &repository.FilterCondition{Field: "title", Op: "contains", Value: tok}
}

// fieldCondition maps a field prefix to the right FilterCondition op.
func fieldCondition(field, value string) *repository.FilterCondition {
	switch field {
	case "type", "media_type":
		return &repository.FilterCondition{Field: "type", Op: "equals", Value: value}
	case "tag":
		return &repository.FilterCondition{Field: "tag", Op: "equals", Value: value}
	case "genre":
		return &repository.FilterCondition{Field: "genre", Op: "equals", Value: value}
	case "contributor", "author":
		return &repository.FilterCondition{Field: "contributor", Op: "contains", Value: value}
	case "title":
		return &repository.FilterCondition{Field: "title", Op: "contains", Value: value}
	case "isbn":
		return &repository.FilterCondition{Field: "title", Op: "contains", Value: value} // ISBN handled separately via repo opts
	case "letter":
		return &repository.FilterCondition{Field: "letter", Op: "equals", Value: value}
	case "series":
		return &repository.FilterCondition{Field: "series", Op: "equals", Value: value}
	case "shelf":
		return &repository.FilterCondition{Field: "shelf", Op: "equals", Value: value}
	case "publisher":
		return &repository.FilterCondition{Field: "publisher", Op: "equals", Value: value}
	case "language":
		return &repository.FilterCondition{Field: "language", Op: "equals", Value: value}
	case "has":
		switch strings.ToLower(value) {
		case "cover":
			return &repository.FilterCondition{Field: "has_cover", Op: "equals", Value: ""}
		}
		return nil
	default:
		// Unknown field — treat as title search
		return &repository.FilterCondition{Field: "title", Op: "contains", Value: field + ":" + value}
	}
}

func negateOp(op string) string {
	switch op {
	case "contains":
		return "not_contains"
	case "not_contains":
		return "contains"
	case "equals":
		return "not_equals"
	case "not_equals":
		return "equals"
	case "phrase":
		return "not_contains" // negate phrase → not_contains
	case "regex":
		return "not_contains" // negate regex → not_contains (simplified)
	default:
		return op
	}
}
