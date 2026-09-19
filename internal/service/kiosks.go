// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package service

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/fireball1725/librarium-api/internal/auth"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A kiosk is an iPad on a library's wall (plans/ipad-kiosk.md). It signs in
// with its own scoped API token, and a member signs in on it by approving a
// QR code with their phone or by entering a PIN. Either way the member gets a
// session that is itself an API token: scoped to borrowing and reading, and
// dead after 15 minutes.
//
// Both kinds of token are PATs, so everything that already enforces token
// scopes applies, and a token can't mint another token.

const (
	kioskCodeTTL    = 60 * time.Second
	kioskSessionTTL = 15 * time.Minute
	pinMaxFailures  = 5
	pinLockout      = 5 * time.Minute
)

// KioskDeviceScopes is what the iPad may do on its own: look things up, and
// lend and take back books.
var KioskDeviceScopes = []string{
	"library:read", "books:read", "editions:read", "contributors:read", "series:read",
	"covers:read", "tags:read", "shelves:read", "loans:read", "loans:create", "loans:update",
}

// KioskMemberScopes is what a signed-in member may do at the kiosk: the same,
// plus their wishlist. Their own lists live under /me.
var KioskMemberScopes = append(append([]string{}, KioskDeviceScopes...), "wishlist:read", "wishlist:create")

var (
	ErrNotAKiosk        = errors.New("this token doesn't belong to a kiosk")
	ErrInteractiveOnly  = errors.New("this needs a signed-in session, not an API token")
	ErrKioskSettings    = errors.New("a kiosk needs a name of 1 to 64 characters, and an idle time of 15 to 600 seconds")
	ErrCodeExpired      = errors.New("that sign-in code has expired or was already used")
	ErrNotAKioskSession = errors.New("this isn't a kiosk session")
	ErrBadPIN           = errors.New("a PIN is 4 to 8 digits")
	ErrNoPIN            = errors.New("that member hasn't set a PIN")
	ErrWrongPIN         = errors.New("wrong PIN")
	ErrPINLocked        = errors.New("too many wrong PINs; try again in a few minutes")
	pinPattern          = regexp.MustCompile(`^[0-9]{4,8}$`)
	codeEncoding        = base32.StdEncoding.WithPadding(base32.NoPadding)
)

// Caller is who's asking: a user, and whether they came in on an API token
// (a kiosk or a kiosk session) or an interactive session.
type Caller struct {
	UserID    uuid.UUID
	TokenID   uuid.UUID // the API token's id when FromToken
	FromToken bool
}

type KioskService struct {
	db     *pgxpool.Pool
	kiosks *repository.KioskRepo
	tokens *repository.APITokenRepo
}

func NewKioskService(db *pgxpool.Pool, kiosks *repository.KioskRepo, tokens *repository.APITokenRepo) *KioskService {
	return &KioskService{db: db, kiosks: kiosks, tokens: tokens}
}

// ── Admin ────────────────────────────────────────────────────────────────────

func validKioskSettings(s repository.KioskSettings, creating bool) error {
	if s.Name != nil {
		n := len([]rune(strings.TrimSpace(*s.Name)))
		if n < 1 || n > 64 {
			return ErrKioskSettings
		}
	} else if creating {
		return ErrKioskSettings
	}
	if s.IdleSeconds != nil && (*s.IdleSeconds < 15 || *s.IdleSeconds > 600) {
		return ErrKioskSettings
	}
	return nil
}

// Register adds a kiosk to a library and mints its token, which is returned
// once and never again. Only from an interactive session, for the same reason
// a token can't mint a token.
func (s *KioskService) Register(ctx context.Context, c *Caller, libraryID uuid.UUID, set repository.KioskSettings) (*models.Kiosk, string, error) {
	if c == nil || c.FromToken {
		return nil, "", ErrInteractiveOnly
	}
	if err := validKioskSettings(set, true); err != nil {
		return nil, "", err
	}
	name := strings.TrimSpace(*set.Name)
	set.Name = &name
	minted, err := repository.Generate(c.UserID, "Kiosk: "+name, KioskDeviceScopes, nil)
	if err != nil {
		return nil, "", err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("beginning kiosk registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.tokens.CreateTx(ctx, tx, minted.Token); err != nil {
		return nil, "", err
	}
	k, err := s.kiosks.Create(ctx, tx, libraryID, minted.Token.ID, c.UserID, set)
	if err != nil {
		return nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", fmt.Errorf("committing kiosk registration: %w", err)
	}
	return k, minted.Raw, nil
}

// Update changes a kiosk's settings.
func (s *KioskService) Update(ctx context.Context, libraryID, id uuid.UUID, set repository.KioskSettings) (*models.Kiosk, error) {
	if err := validKioskSettings(set, false); err != nil {
		return nil, err
	}
	if set.Name != nil {
		name := strings.TrimSpace(*set.Name)
		set.Name = &name
	}
	return s.kiosks.Update(ctx, libraryID, id, set)
}

func (s *KioskService) List(ctx context.Context, libraryID uuid.UUID) ([]*models.Kiosk, error) {
	return s.kiosks.ListForLibrary(ctx, libraryID)
}

// Delete removes a kiosk. Its token goes with it, and so does anyone signed in
// on it.
func (s *KioskService) Delete(ctx context.Context, libraryID, id uuid.UUID) error {
	return s.kiosks.Delete(ctx, libraryID, id)
}

// ── The kiosk itself ─────────────────────────────────────────────────────────

// Me is the kiosk the calling token belongs to.
func (s *KioskService) Me(ctx context.Context, c *Caller) (*models.Kiosk, error) {
	if c == nil || !c.FromToken {
		return nil, ErrNotAKiosk
	}
	k, err := s.kiosks.ByToken(ctx, c.TokenID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrNotAKiosk
	}
	return k, err
}

// Members lists who can sign in on the kiosk, for its picker.
func (s *KioskService) Members(ctx context.Context, k *models.Kiosk) ([]*models.KioskMember, error) {
	return s.kiosks.Members(ctx, k.LibraryID)
}

// NewCode makes a sign-in code for the kiosk to show as a QR code. It's 130
// random bits: guessing a live one isn't a practical way into someone else's
// kiosk.
func (s *KioskService) NewCode(ctx context.Context, k *models.Kiosk) (string, time.Time, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", time.Time{}, fmt.Errorf("random sign-in code: %w", err)
	}
	code := codeEncoding.EncodeToString(buf)
	expires := time.Now().Add(kioskCodeTTL)
	if err := s.kiosks.CreateCode(ctx, k.ID, code, expires); err != nil {
		return "", time.Time{}, err
	}
	return code, expires, nil
}

// KioskSession is a member signed in on a kiosk.
type KioskSession struct {
	Token       string    `json:"token"`
	ExpiresAt   time.Time `json:"expires_at"`
	UserID      uuid.UUID `json:"user_id"`
	DisplayName string    `json:"display_name"`
}

// CodeStatus is what the kiosk sees when it polls its code.
type CodeStatus struct {
	Status  string        `json:"status"` // pending, approved, expired, used
	Session *KioskSession `json:"session,omitempty"`
}

// PollCode tells the kiosk whether its code was approved. The first poll after
// approval claims the code and hands over the member's session; the code is
// used up after that.
func (s *KioskService) PollCode(ctx context.Context, k *models.Kiosk, code string) (*CodeStatus, error) {
	c, err := s.kiosks.Code(ctx, code)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && c.KioskID != k.ID) {
		return &CodeStatus{Status: "expired"}, nil
	}
	if err != nil {
		return nil, err
	}
	switch {
	case c.ClaimedAt != nil:
		return &CodeStatus{Status: "used"}, nil
	case c.ApprovedBy == nil && time.Now().After(c.ExpiresAt):
		return &CodeStatus{Status: "expired"}, nil
	case c.ApprovedBy == nil:
		return &CodeStatus{Status: "pending"}, nil
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning kiosk sign-in: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ok, err := s.kiosks.ClaimCode(ctx, tx, code, k.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &CodeStatus{Status: "used"}, nil
	}
	session, err := s.startSession(ctx, tx, k, *c.ApprovedBy, "phone")
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing kiosk sign-in: %w", err)
	}
	return &CodeStatus{Status: "approved", Session: session}, nil
}

// PINSignin signs a member in on the kiosk with their PIN. Five wrong PINs in
// five minutes lock that member out on that kiosk until they age out.
func (s *KioskService) PINSignin(ctx context.Context, k *models.Kiosk, userID uuid.UUID, pin string) (*KioskSession, error) {
	member, err := s.kiosks.IsMember(ctx, k.LibraryID, userID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrNotLibraryMember
	}
	failures, err := s.kiosks.RecentPINFailures(ctx, k.ID, userID, time.Now().Add(-pinLockout))
	if err != nil {
		return nil, err
	}
	if failures >= pinMaxFailures {
		return nil, ErrPINLocked
	}
	hash, err := s.kiosks.PINHash(ctx, userID)
	if err != nil {
		return nil, err
	}
	if hash == "" {
		return nil, ErrNoPIN
	}
	if auth.VerifyPassword(hash, pin) != nil {
		if err := s.kiosks.AddPINFailure(ctx, k.ID, userID); err != nil {
			return nil, err
		}
		return nil, ErrWrongPIN
	}
	if err := s.kiosks.ClearPINFailures(ctx, k.ID, userID); err != nil {
		return nil, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning kiosk sign-in: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	session, err := s.startSession(ctx, tx, k, userID, "pin")
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing kiosk sign-in: %w", err)
	}
	return session, nil
}

func (s *KioskService) startSession(ctx context.Context, tx pgx.Tx, k *models.Kiosk, userID uuid.UUID, method string) (*KioskSession, error) {
	var name string
	if err := tx.QueryRow(ctx, `SELECT display_name FROM users WHERE id = $1 AND is_active`, userID).Scan(&name); err != nil {
		return nil, ErrNotLibraryMember
	}
	expires := time.Now().Add(kioskSessionTTL)
	minted, err := repository.Generate(userID, "Kiosk session: "+k.Name, KioskMemberScopes, &expires)
	if err != nil {
		return nil, err
	}
	if err := s.tokens.CreateTx(ctx, tx, minted.Token); err != nil {
		return nil, err
	}
	if err := s.kiosks.AddSession(ctx, tx, minted.Token.ID, k.ID, userID, method); err != nil {
		return nil, err
	}
	return &KioskSession{Token: minted.Raw, ExpiresAt: expires, UserID: userID, DisplayName: name}, nil
}

// ── The member's phone ───────────────────────────────────────────────────────

// CodePreview is what the phone shows before the member approves.
type CodePreview struct {
	KioskName   string    `json:"kiosk_name"`
	LibraryID   uuid.UUID `json:"library_id"`
	LibraryName string    `json:"library_name"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (s *KioskService) liveCodeForMember(ctx context.Context, userID uuid.UUID, code string) (*repository.SigninCode, *models.Kiosk, error) {
	c, err := s.kiosks.Code(ctx, code)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil, ErrCodeExpired
	}
	if err != nil {
		return nil, nil, err
	}
	if c.ApprovedBy != nil || time.Now().After(c.ExpiresAt) {
		return nil, nil, ErrCodeExpired
	}
	k, err := s.kiosks.ByID(ctx, c.KioskID)
	if err != nil {
		return nil, nil, err
	}
	member, err := s.kiosks.IsMember(ctx, k.LibraryID, userID)
	if err != nil {
		return nil, nil, err
	}
	if !member {
		return nil, nil, ErrNotLibraryMember
	}
	return c, k, nil
}

// Preview says which kiosk and library a code is for, so the member can check
// before approving.
func (s *KioskService) Preview(ctx context.Context, c *Caller, code string) (*CodePreview, error) {
	sc, k, err := s.liveCodeForMember(ctx, c.UserID, code)
	if err != nil {
		return nil, err
	}
	var libName string
	if err := s.db.QueryRow(ctx, `SELECT name FROM libraries WHERE id = $1`, k.LibraryID).Scan(&libName); err != nil {
		return nil, fmt.Errorf("reading library name: %w", err)
	}
	return &CodePreview{KioskName: k.Name, LibraryID: k.LibraryID, LibraryName: libName, ExpiresAt: sc.ExpiresAt}, nil
}

// Approve signs the member in on the kiosk that shows the code. Only from an
// interactive session: a kiosk session can't approve a code for another kiosk.
func (s *KioskService) Approve(ctx context.Context, c *Caller, code string) error {
	if c == nil || c.FromToken {
		return ErrInteractiveOnly
	}
	if _, _, err := s.liveCodeForMember(ctx, c.UserID, code); err != nil {
		return err
	}
	ok, err := s.kiosks.ApproveCode(ctx, code, c.UserID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrCodeExpired
	}
	return nil
}

// EndSession signs the member out of the kiosk: the kiosk calls it with the
// member's session on sign-out and on idle reset.
func (s *KioskService) EndSession(ctx context.Context, c *Caller) error {
	if c == nil || !c.FromToken {
		return ErrNotAKioskSession
	}
	ok, err := s.kiosks.EndSession(ctx, c.TokenID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotAKioskSession
	}
	return nil
}

// ── PINs ─────────────────────────────────────────────────────────────────────

// SetPIN sets the member's PIN, hashed like a password. It's for quick
// sign-in where a password isn't practical; the kiosk is the first user. Only
// from an interactive session, so a kiosk session can't change it.
func (s *KioskService) SetPIN(ctx context.Context, c *Caller, pin string) error {
	if c == nil || c.FromToken {
		return ErrInteractiveOnly
	}
	if !pinPattern.MatchString(pin) {
		return ErrBadPIN
	}
	hash, err := auth.HashPassword(pin)
	if err != nil {
		return err
	}
	return s.kiosks.SetPINHash(ctx, c.UserID, &hash)
}

// ClearPIN removes the member's PIN.
func (s *KioskService) ClearPIN(ctx context.Context, c *Caller) error {
	if c == nil || c.FromToken {
		return ErrInteractiveOnly
	}
	return s.kiosks.SetPINHash(ctx, c.UserID, nil)
}

// HasPIN says whether the member has set a PIN.
func (s *KioskService) HasPIN(ctx context.Context, userID uuid.UUID) (bool, error) {
	hash, err := s.kiosks.PINHash(ctx, userID)
	return hash != "", err
}
