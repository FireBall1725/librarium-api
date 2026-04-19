// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import "testing"

func TestExtractSeriesBase(t *testing.T) {
	tests := []struct {
		title    string
		wantBase string
		wantPos  float64
		wantOK   bool
	}{
		{"Naruto, Vol. 3", "Naruto", 3, true},
		{"Naruto Vol. 7", "Naruto", 7, true},
		{"One Piece Vol. 12", "One Piece", 12, true},
		{"Bleach #5: The Death Trilogy Overture", "Bleach", 5, true},
		{"Attack on Titan Volume 2", "Attack on Titan", 2, true},
		{"Berserk, Volume 1: The Black Swordsman", "Berserk", 1, true},
		{"Vinland Saga Book 4", "Vinland Saga", 4, true},
		{"Akira 4", "Akira", 4, true},
		{"Vinland Saga 1", "Vinland Saga", 1, true},
		{"Chainsaw Man 1.5", "Chainsaw Man", 1.5, true},
		{"Akira, 2", "Akira", 2, true},
		{"The Stand", "", 0, false},
		{"1984", "", 0, false},
		{"Yearbook 2024", "", 0, false}, // over cap
		{"", "", 0, false},
		{"   ", "", 0, false},
		{"Blame! 1", "Blame!", 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			gotBase, gotPos, gotOK := ExtractSeriesBase(tt.title)
			if gotOK != tt.wantOK {
				t.Fatalf("ok = %v, want %v (base=%q, pos=%v)", gotOK, tt.wantOK, gotBase, gotPos)
			}
			if gotOK {
				if gotBase != tt.wantBase {
					t.Errorf("base = %q, want %q", gotBase, tt.wantBase)
				}
				if gotPos != tt.wantPos {
					t.Errorf("pos = %v, want %v", gotPos, tt.wantPos)
				}
			}
		})
	}
}

func TestNormalizeSeriesKey(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Naruto", "naruto"},
		{"  Naruto  ", "naruto"},
		{"One   Piece", "one piece"},
		{"ONE PIECE", "one piece"},
	}
	for _, tt := range tests {
		if got := NormalizeSeriesKey(tt.in); got != tt.want {
			t.Errorf("NormalizeSeriesKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
