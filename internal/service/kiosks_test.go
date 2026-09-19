// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/fireball1725/librarium-api/internal/repository"
)

func TestKioskSettingsValidation(t *testing.T) {
	str := func(s string) *string { return &s }
	num := func(n int) *int { return &n }
	cases := []struct {
		s        repository.KioskSettings
		creating bool
		ok       bool
	}{
		{repository.KioskSettings{Name: str("Hall")}, true, true},
		{repository.KioskSettings{}, true, false}, // a new kiosk needs a name
		{repository.KioskSettings{}, false, true}, // an update can leave it
		{repository.KioskSettings{Name: str("   ")}, true, false},
		{repository.KioskSettings{Name: str("Hall"), IdleSeconds: num(14)}, true, false},
		{repository.KioskSettings{Name: str("Hall"), IdleSeconds: num(600)}, true, true},
	}
	for i, c := range cases {
		if err := validKioskSettings(c.s, c.creating); (err == nil) != c.ok {
			t.Errorf("case %d: err = %v", i, err)
		}
	}
}

// Setting a PIN or registering a kiosk needs a signed-in session: a kiosk
// session must not be able to change the member's PIN.
func TestKioskInteractiveOnly(t *testing.T) {
	s := &KioskService{}
	token := &Caller{FromToken: true}
	if err := s.SetPIN(context.Background(), token, "1234"); !errors.Is(err, ErrInteractiveOnly) {
		t.Errorf("SetPIN from a token: %v", err)
	}
	if err := s.ClearPIN(context.Background(), token); !errors.Is(err, ErrInteractiveOnly) {
		t.Errorf("ClearPIN from a token: %v", err)
	}
	if err := s.Approve(context.Background(), token, "code"); !errors.Is(err, ErrInteractiveOnly) {
		t.Errorf("Approve from a token: %v", err)
	}
	if _, _, err := s.Register(context.Background(), token, [16]byte{}, repository.KioskSettings{}); !errors.Is(err, ErrInteractiveOnly) {
		t.Errorf("Register from a token: %v", err)
	}
	for _, pin := range []string{"123", "123456789", "12a4", ""} {
		if err := s.SetPIN(context.Background(), &Caller{}, pin); !errors.Is(err, ErrBadPIN) {
			t.Errorf("PIN %q: %v", pin, err)
		}
	}
}
