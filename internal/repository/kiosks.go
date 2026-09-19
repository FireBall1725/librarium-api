// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// KioskRepo holds kiosks, their sign-in codes and sessions, and members' PINs.
// See migration 44.
type KioskRepo struct {
	db *pgxpool.Pool
}

func NewKioskRepo(db *pgxpool.Pool) *KioskRepo {
	return &KioskRepo{db: db}
}

const kioskColumns = `id, library_id, name, clock_24h, allow_anonymous, allow_signup,
	show_borrower, idle_seconds, last_seen_at, created_at`

func scanKiosk(row pgx.Row) (*models.Kiosk, error) {
	var k models.Kiosk
	err := row.Scan(&k.ID, &k.LibraryID, &k.Name, &k.Clock24h, &k.AllowAnonymous, &k.AllowSignup,
		&k.ShowBorrower, &k.IdleSeconds, &k.LastSeenAt, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning kiosk: %w", err)
	}
	return &k, nil
}

// KioskSettings are the fields an admin sets. Create takes them all; Update
// changes the ones that are set.
type KioskSettings struct {
	Name           *string
	SetClock24h    bool
	Clock24h       *bool
	AllowAnonymous *bool
	AllowSignup    *bool
	ShowBorrower   *bool
	IdleSeconds    *int
}

// Create registers a kiosk against a token already inserted in tx.
func (r *KioskRepo) Create(ctx context.Context, tx pgx.Tx, libraryID, tokenID uuid.UUID, createdBy uuid.UUID, s KioskSettings) (*models.Kiosk, error) {
	name := ""
	if s.Name != nil {
		name = *s.Name
	}
	boolOr := func(p *bool, d bool) bool {
		if p == nil {
			return d
		}
		return *p
	}
	idle := 60
	if s.IdleSeconds != nil {
		idle = *s.IdleSeconds
	}
	return scanKiosk(tx.QueryRow(ctx, `
		INSERT INTO kiosks (library_id, api_token_id, name, clock_24h, allow_anonymous, allow_signup, show_borrower, idle_seconds, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+kioskColumns,
		libraryID, tokenID, name, s.Clock24h, boolOr(s.AllowAnonymous, false), boolOr(s.AllowSignup, false),
		boolOr(s.ShowBorrower, true), idle, createdBy))
}

// Update changes the given settings on a kiosk in a library.
func (r *KioskRepo) Update(ctx context.Context, libraryID, id uuid.UUID, s KioskSettings) (*models.Kiosk, error) {
	return scanKiosk(r.db.QueryRow(ctx, `
		UPDATE kiosks SET
		    name            = COALESCE($3, name),
		    clock_24h       = CASE WHEN $4 THEN $5 ELSE clock_24h END,
		    allow_anonymous = COALESCE($6, allow_anonymous),
		    allow_signup    = COALESCE($7, allow_signup),
		    show_borrower   = COALESCE($8, show_borrower),
		    idle_seconds    = COALESCE($9, idle_seconds)
		 WHERE id = $2 AND library_id = $1
		RETURNING `+kioskColumns,
		libraryID, id, s.Name, s.SetClock24h, s.Clock24h, s.AllowAnonymous, s.AllowSignup, s.ShowBorrower, s.IdleSeconds))
}

// ListForLibrary lists a library's kiosks, oldest first.
func (r *KioskRepo) ListForLibrary(ctx context.Context, libraryID uuid.UUID) ([]*models.Kiosk, error) {
	rows, err := r.db.Query(ctx, `SELECT `+kioskColumns+` FROM kiosks WHERE library_id = $1 ORDER BY created_at, id`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("listing kiosks: %w", err)
	}
	defer rows.Close()
	out := make([]*models.Kiosk, 0)
	for rows.Next() {
		k, err := scanKiosk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Delete removes a kiosk and its token, which ends its sessions with it.
func (r *KioskRepo) Delete(ctx context.Context, libraryID, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `
		DELETE FROM api_tokens
		 WHERE id = (SELECT api_token_id FROM kiosks WHERE id = $2 AND library_id = $1)
		    OR id IN (SELECT s.api_token_id FROM kiosk_sessions s
		               JOIN kiosks k ON k.id = s.kiosk_id WHERE k.id = $2 AND k.library_id = $1)`,
		libraryID, id)
	if err != nil {
		return fmt.Errorf("deleting kiosk: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ByToken finds the kiosk a token belongs to, and notes it was seen.
func (r *KioskRepo) ByToken(ctx context.Context, tokenID uuid.UUID) (*models.Kiosk, error) {
	return scanKiosk(r.db.QueryRow(ctx, `
		UPDATE kiosks SET last_seen_at = NOW() WHERE api_token_id = $1
		RETURNING `+kioskColumns, tokenID))
}

// ByID finds a kiosk.
func (r *KioskRepo) ByID(ctx context.Context, id uuid.UUID) (*models.Kiosk, error) {
	return scanKiosk(r.db.QueryRow(ctx, `SELECT `+kioskColumns+` FROM kiosks WHERE id = $1`, id))
}

// IsMember says whether a user can be signed in on a library's kiosk: an
// active user holding a role in that library, or an instance-wide one.
func (r *KioskRepo) IsMember(ctx context.Context, libraryID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM user_roles ur JOIN users u ON u.id = ur.user_id
		     WHERE ur.user_id = $2 AND u.is_active
		       AND (ur.library_id = $1 OR ur.library_id IS NULL))`, libraryID, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("checking kiosk membership: %w", err)
	}
	return ok, nil
}

// Members lists who can sign in on a library's kiosk, for its picker.
func (r *KioskRepo) Members(ctx context.Context, libraryID uuid.UUID) ([]*models.KioskMember, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT u.id, u.display_name, u.pin_hash IS NOT NULL
		  FROM users u JOIN user_roles ur ON ur.user_id = u.id
		 WHERE u.is_active AND (ur.library_id = $1 OR ur.library_id IS NULL)
		 ORDER BY u.display_name, u.id`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("listing kiosk members: %w", err)
	}
	defer rows.Close()
	out := make([]*models.KioskMember, 0)
	for rows.Next() {
		var m models.KioskMember
		if err := rows.Scan(&m.UserID, &m.DisplayName, &m.HasPIN); err != nil {
			return nil, fmt.Errorf("scanning kiosk member: %w", err)
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// ── Sign-in codes ────────────────────────────────────────────────────────────

// SigninCode is a code a kiosk shows for a phone to approve.
type SigninCode struct {
	Code       string
	KioskID    uuid.UUID
	ExpiresAt  time.Time
	ApprovedBy *uuid.UUID
	ClaimedAt  *time.Time
}

// CreateCode stores a new code, and sweeps out the ones that are long dead.
func (r *KioskRepo) CreateCode(ctx context.Context, kioskID uuid.UUID, code string, expiresAt time.Time) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM kiosk_signin_codes WHERE expires_at < NOW() - INTERVAL '1 hour'`); err != nil {
		return fmt.Errorf("sweeping sign-in codes: %w", err)
	}
	if _, err := r.db.Exec(ctx, `INSERT INTO kiosk_signin_codes (code, kiosk_id, expires_at) VALUES ($1, $2, $3)`,
		code, kioskID, expiresAt); err != nil {
		return fmt.Errorf("creating sign-in code: %w", err)
	}
	return nil
}

// Code finds a sign-in code.
func (r *KioskRepo) Code(ctx context.Context, code string) (*SigninCode, error) {
	var c SigninCode
	err := r.db.QueryRow(ctx, `
		SELECT code, kiosk_id, expires_at, approved_by, claimed_at FROM kiosk_signin_codes WHERE code = $1`, code).
		Scan(&c.Code, &c.KioskID, &c.ExpiresAt, &c.ApprovedBy, &c.ClaimedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading sign-in code: %w", err)
	}
	return &c, nil
}

// ApproveCode marks a live, unapproved code as approved by a member. ok is
// false when the code expired or someone got there first.
func (r *KioskRepo) ApproveCode(ctx context.Context, code string, userID uuid.UUID) (ok bool, err error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE kiosk_signin_codes SET approved_by = $2, approved_at = NOW()
		 WHERE code = $1 AND approved_by IS NULL AND expires_at > NOW()`, code, userID)
	if err != nil {
		return false, fmt.Errorf("approving sign-in code: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ClaimCode marks an approved code as used, once, in tx. ok is false if it was
// already claimed.
func (r *KioskRepo) ClaimCode(ctx context.Context, tx pgx.Tx, code string, kioskID uuid.UUID) (ok bool, err error) {
	tag, err := tx.Exec(ctx, `
		UPDATE kiosk_signin_codes SET claimed_at = NOW()
		 WHERE code = $1 AND kiosk_id = $2 AND approved_by IS NOT NULL AND claimed_at IS NULL`, code, kioskID)
	if err != nil {
		return false, fmt.Errorf("claiming sign-in code: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ── Sessions ─────────────────────────────────────────────────────────────────

// AddSession records a member session token as belonging to a kiosk, in tx.
func (r *KioskRepo) AddSession(ctx context.Context, tx pgx.Tx, tokenID, kioskID, userID uuid.UUID, method string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO kiosk_sessions (api_token_id, kiosk_id, user_id, method) VALUES ($1, $2, $3, $4)`,
		tokenID, kioskID, userID, method); err != nil {
		return fmt.Errorf("recording kiosk session: %w", err)
	}
	return nil
}

// EndSession revokes a kiosk member session token. ok is false when the token
// isn't a kiosk session.
func (r *KioskRepo) EndSession(ctx context.Context, tokenID uuid.UUID) (ok bool, err error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE api_tokens SET revoked_at = NOW()
		 WHERE id = $1 AND revoked_at IS NULL
		   AND EXISTS (SELECT 1 FROM kiosk_sessions WHERE api_token_id = $1)`, tokenID)
	if err != nil {
		return false, fmt.Errorf("ending kiosk session: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ── PINs ─────────────────────────────────────────────────────────────────────

// SetPINHash sets or, with nil, clears a user's PIN.
func (r *KioskRepo) SetPINHash(ctx context.Context, userID uuid.UUID, hash *string) error {
	if _, err := r.db.Exec(ctx, `UPDATE users SET pin_hash = $2, updated_at = NOW() WHERE id = $1`, userID, hash); err != nil {
		return fmt.Errorf("setting pin: %w", err)
	}
	return nil
}

// PINHash reads a user's PIN hash; empty when they have none.
func (r *KioskRepo) PINHash(ctx context.Context, userID uuid.UUID) (string, error) {
	var hash *string
	err := r.db.QueryRow(ctx, `SELECT pin_hash FROM users WHERE id = $1`, userID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("reading pin: %w", err)
	}
	if hash == nil {
		return "", nil
	}
	return *hash, nil
}

// RecentPINFailures counts wrong PINs for a member on a kiosk since a time.
func (r *KioskRepo) RecentPINFailures(ctx context.Context, kioskID, userID uuid.UUID, since time.Time) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT count(*) FROM kiosk_pin_failures WHERE kiosk_id = $1 AND user_id = $2 AND failed_at > $3`,
		kioskID, userID, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting pin failures: %w", err)
	}
	return n, nil
}

// AddPINFailure records a wrong PIN, and drops failures older than a day.
func (r *KioskRepo) AddPINFailure(ctx context.Context, kioskID, userID uuid.UUID) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM kiosk_pin_failures WHERE failed_at < NOW() - INTERVAL '1 day'`); err != nil {
		return fmt.Errorf("sweeping pin failures: %w", err)
	}
	if _, err := r.db.Exec(ctx, `INSERT INTO kiosk_pin_failures (kiosk_id, user_id) VALUES ($1, $2)`, kioskID, userID); err != nil {
		return fmt.Errorf("recording pin failure: %w", err)
	}
	return nil
}

// ClearPINFailures forgets a member's wrong PINs on a kiosk after a right one.
func (r *KioskRepo) ClearPINFailures(ctx context.Context, kioskID, userID uuid.UUID) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM kiosk_pin_failures WHERE kiosk_id = $1 AND user_id = $2`, kioskID, userID); err != nil {
		return fmt.Errorf("clearing pin failures: %w", err)
	}
	return nil
}
