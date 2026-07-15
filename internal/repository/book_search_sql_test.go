// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"strings"
	"testing"
)

func TestTitleContainsSQLGuardsEmptyNormalisation(t *testing.T) {
	sql := titleContainsSQL(2, asciiNormSpace)
	if !strings.Contains(sql, "<> ''") {
		t.Fatalf("expected empty-normalisation guard, got: %s", sql)
	}
	if !strings.Contains(sql, asciiNormSpace) {
		t.Fatalf("expected norm pattern %q in SQL", asciiNormSpace)
	}
}

func TestTitleContainsSQLUsesConsecutiveArguments(t *testing.T) {
	sql := titleContainsSQL(2, asciiNormSpace)

	for _, expected := range []string{"$2", "$3", "$4"} {
		if !strings.Contains(sql, expected) {
			t.Errorf("expected SQL to contain %s, got: %s", expected, sql)
		}
	}
}

func TestTitleContainsSQLGuardsNonAsciiQuery(t *testing.T) {
	sql := titleContainsSQL(1, asciiNormSpace)

	if !strings.Contains(sql, "regexp_replace(lower($2)") {
		t.Fatalf("expected normalised fallback to use second argument, got: %s", sql)
	}
	if !strings.Contains(sql, "|| $3 ||") {
		t.Fatalf("expected contributor search to use third argument, got: %s", sql)
	}
}

func TestTitlePhraseSQLSkipsAsciiFallback(t *testing.T) {
	sql := titlePhraseSQL(3)
	if strings.Contains(sql, "regexp_replace") {
		t.Fatalf("phrase match should not use regexp fallback: %s", sql)
	}
}

func TestTitlePhraseSQLUsesConsecutiveArguments(t *testing.T) {
	sql := titlePhraseSQL(3)

	for _, expected := range []string{"$3", "$4"} {
		if !strings.Contains(sql, expected) {
			t.Errorf("expected SQL to contain %s, got: %s", expected, sql)
		}
	}
}
