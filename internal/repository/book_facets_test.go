// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import "testing"

func names(vals []FacetValue) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = v.Value
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestFillClosedZeroFills is the behaviour the rail depends on: a fixed
// vocabulary is offered whole, so a filter does not vanish precisely when its
// answer is none.
func TestFillClosedZeroFills(t *testing.T) {
	got := fillClosed(
		[]FacetValue{{Value: "shelf", Label: "shelf", Count: 1425}},
		OwnershipValues,
	)
	if !equal(names(got), OwnershipValues) {
		t.Errorf("got %v, want the whole vocabulary %v", names(got), OwnershipValues)
	}
	for _, v := range got {
		switch v.Value {
		case "shelf":
			if v.Count != 1425 {
				t.Errorf("shelf count = %d, want the counted 1425", v.Count)
			}
		default:
			if v.Count != 0 {
				t.Errorf("%s count = %d, want 0", v.Value, v.Count)
			}
			if v.Label != v.Value {
				t.Errorf("%s label = %q, want the value as a fallback label", v.Value, v.Label)
			}
		}
	}
}

// TestFillClosedKeepsVocabularyOrder guards the display order, which is the
// order a reader reads: what you have, then what you want, then what was
// proposed, then what is missing.
func TestFillClosedKeepsVocabularyOrder(t *testing.T) {
	// Deliberately supplied in the wrong order, as a GROUP BY may well return it.
	got := fillClosed([]FacetValue{
		{Value: "gap", Count: 3},
		{Value: "shelf", Count: 2},
		{Value: "wishlist", Count: 1},
		{Value: "suggested", Count: 4},
	}, OwnershipValues)
	if !equal(names(got), OwnershipValues) {
		t.Errorf("got %v, want %v", names(got), OwnershipValues)
	}
}

// TestFillClosedKeepsUnknownValues covers the case that would otherwise hide a
// schema change: a value the database holds that the vocabulary has not heard
// of is kept on the end rather than dropped.
func TestFillClosedKeepsUnknownValues(t *testing.T) {
	got := fillClosed([]FacetValue{
		{Value: "shelf", Count: 5},
		{Value: "borrowed", Count: 2},
	}, OwnershipValues)

	want := append(append([]string{}, OwnershipValues...), "borrowed")
	if !equal(names(got), want) {
		t.Errorf("got %v, want %v", names(got), want)
	}
	if got[len(got)-1].Count != 2 {
		t.Errorf("unknown value lost its count: %d", got[len(got)-1].Count)
	}
}

// TestFillClosedIsIdempotent — the facet is filled once per request today, but
// filling an already-filled slice must not duplicate anything.
func TestFillClosedIsIdempotent(t *testing.T) {
	once := fillClosed([]FacetValue{{Value: "shelf", Count: 9}}, OwnershipValues)
	twice := fillClosed(once, OwnershipValues)
	if !equal(names(once), names(twice)) {
		t.Errorf("second pass changed the set: %v then %v", names(once), names(twice))
	}
}

// TestReadStatusVocabularyIsComplete pins the read statuses the rest of the
// repository writes, so adding one to the schema without adding it here fails
// rather than quietly dropping out of the rail.
func TestReadStatusVocabularyIsComplete(t *testing.T) {
	for _, want := range []string{"unread", "reading", "read", "did_not_finish"} {
		found := false
		for _, got := range ReadStatusValues {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("ReadStatusValues is missing %q", want)
		}
	}
}
