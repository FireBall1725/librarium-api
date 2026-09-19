// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package providers

import (
	"reflect"
	"testing"
)

// librarium-web #84: "Author, Jr." was saved as two authors.
func TestSplitAuthorNamesKeepsSuffixes(t *testing.T) {
	cases := map[string][]string{
		"Martin Luther King, Jr.":                     {"Martin Luther King, Jr."},
		"Martin Luther King, Jr., Coretta Scott King": {"Martin Luther King, Jr.", "Coretta Scott King"},
		"Neil Gaiman, Terry Pratchett":                {"Neil Gaiman", "Terry Pratchett"},
		"Harry Connick, jr":                           {"Harry Connick, jr"},
		"Ken Griffey, Sr, Ken Griffey, Jr.":           {"Ken Griffey, Sr", "Ken Griffey, Jr."},
		"Henry VIII, III":                             {"Henry VIII, III"},
		"Jane Smith, PhD, John Doe, M.D.":             {"Jane Smith, PhD", "John Doe, M.D."},
		" , Ann Leckie ,, ":                           {"Ann Leckie"},
		"":                                            nil,
		// A suffix with nothing before it is kept as it is rather than lost.
		"Jr., Someone": {"Jr.", "Someone"},
	}
	for in, want := range cases {
		if got := SplitAuthorNames(in); !reflect.DeepEqual(got, want) {
			t.Errorf("SplitAuthorNames(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinNameSuffixesFoldsASplitProviderList(t *testing.T) {
	got := JoinNameSuffixes([]string{"Martin Luther King", "Jr.", " Coretta Scott King "})
	want := []string{"Martin Luther King, Jr.", "Coretta Scott King"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMergedAuthorsCarryTheirList(t *testing.T) {
	merged := MergeBookResults([]*BookResult{
		{Provider: "a", Authors: []string{"Martin Luther King, Jr."}},
		{Provider: "b", Authors: []string{"Martin Luther King, Jr."}},
		{Provider: "c", Authors: []string{"Neil Gaiman", "Terry Pratchett"}},
	})
	if merged.Authors == nil {
		t.Fatal("no authors")
	}
	if got := merged.Authors.AuthorNames(); !reflect.DeepEqual(got, []string{"Martin Luther King, Jr."}) {
		t.Errorf("picked authors = %q", got)
	}
	if len(merged.Authors.Alternatives) != 1 ||
		!reflect.DeepEqual(merged.Authors.Alternatives[0].Values, []string{"Neil Gaiman", "Terry Pratchett"}) {
		t.Errorf("alternatives = %+v", merged.Authors.Alternatives)
	}
}

func TestAuthorNamesFallsBackToSplitting(t *testing.T) {
	// A merged result stored before Values existed has only the joined text.
	f := &FieldResult{Value: "Martin Luther King, Jr., Coretta Scott King"}
	want := []string{"Martin Luther King, Jr.", "Coretta Scott King"}
	if got := f.AuthorNames(); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	var none *FieldResult
	if none.AuthorNames() != nil {
		t.Error("nil field should give no names")
	}
}
