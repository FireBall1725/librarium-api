// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/fireball1725/librarium-api/internal/auth"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrRegistrationDisabled = errors.New("registration is disabled")
	ErrInvalidCredentials   = errors.New("invalid credentials")
	ErrAccountInactive      = errors.New("account is inactive")
	ErrTokenExpired         = errors.New("token is expired")
	ErrTokenRevoked         = errors.New("token has been revoked")
	ErrAlreadyInitialized   = errors.New("instance already initialized")
)

type AuthConfig struct {
	AccessTTL           time.Duration
	RefreshTTL          time.Duration
	RegistrationEnabled bool
}

type AuthResponse struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int // seconds until access token expiry
	User         *models.User
}

type AuthService struct {
	pool       *pgxpool.Pool
	users      *repository.UserRepo
	identities *repository.IdentityRepo
	tokens     *repository.TokenRepo
	denylist   *repository.DenylistRepo
	jwt        *auth.JWTService
	cfg        AuthConfig
}

func NewAuthService(
	pool *pgxpool.Pool,
	users *repository.UserRepo,
	identities *repository.IdentityRepo,
	tokens *repository.TokenRepo,
	denylist *repository.DenylistRepo,
	jwtSvc *auth.JWTService,
	cfg AuthConfig,
) *AuthService {
	return &AuthService{
		pool:       pool,
		users:      users,
		identities: identities,
		tokens:     tokens,
		denylist:   denylist,
		jwt:        jwtSvc,
		cfg:        cfg,
	}
}

type RegisterRequest struct {
	Username    string
	Email       string
	DisplayName string
	Password    string
}

func (s *AuthService) Register(ctx context.Context, req RegisterRequest) (*AuthResponse, error) {
	if !s.cfg.RegistrationEnabled {
		return nil, ErrRegistrationDisabled
	}

	if exists, err := s.users.ExistsByUsername(ctx, req.Username); err != nil {
		return nil, err
	} else if exists {
		return nil, fmt.Errorf("username %w", repository.ErrDuplicate)
	}

	if exists, err := s.users.ExistsByEmail(ctx, req.Email); err != nil {
		return nil, err
	} else if exists {
		return nil, fmt.Errorf("email %w", repository.ErrDuplicate)
	}

	count, err := s.users.Count(ctx)
	if err != nil {
		return nil, err
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	userID := uuid.New()
	identityID := uuid.New()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	user, err := s.users.Create(ctx, tx, userID, req.Username, req.Email, req.DisplayName, count == 0)
	if err != nil {
		return nil, err
	}

	if err := s.identities.CreateLocal(ctx, tx, identityID, userID, req.Username, hash); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return s.issueTokens(ctx, user)
}

// BootstrapAdmin creates the first instance admin on a fresh install.
// Returns ErrAlreadyInitialized if any user already exists. Bypasses the
// RegistrationEnabled config flag so setup is always reachable on an empty DB.
func (s *AuthService) BootstrapAdmin(ctx context.Context, req RegisterRequest) (*AuthResponse, error) {
	count, err := s.users.Count(ctx)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, ErrAlreadyInitialized
	}

	if exists, err := s.users.ExistsByUsername(ctx, req.Username); err != nil {
		return nil, err
	} else if exists {
		return nil, fmt.Errorf("username %w", repository.ErrDuplicate)
	}
	if exists, err := s.users.ExistsByEmail(ctx, req.Email); err != nil {
		return nil, err
	} else if exists {
		return nil, fmt.Errorf("email %w", repository.ErrDuplicate)
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	userID := uuid.New()
	identityID := uuid.New()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	user, err := s.users.Create(ctx, tx, userID, req.Username, req.Email, req.DisplayName, true)
	if err != nil {
		return nil, err
	}
	if err := s.identities.CreateLocal(ctx, tx, identityID, userID, req.Username, hash); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return s.issueTokens(ctx, user)
}

type LoginRequest struct {
	Identifier string // username or email
	Password   string
}

func (s *AuthService) Login(ctx context.Context, req LoginRequest) (*AuthResponse, error) {
	user, err := s.users.FindByUsername(ctx, req.Identifier)
	if errors.Is(err, repository.ErrNotFound) {
		user, err = s.users.FindByEmail(ctx, req.Identifier)
	}
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}

	if !user.IsActive {
		return nil, ErrAccountInactive
	}

	pwHash, err := s.identities.GetLocalPasswordHash(ctx, user.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}

	if err := auth.VerifyPassword(pwHash, req.Password); err != nil {
		return nil, ErrInvalidCredentials
	}

	if err := s.users.UpdateLastLogin(ctx, user.ID); err != nil {
		return nil, err
	}

	return s.issueTokens(ctx, user)
}

func (s *AuthService) Refresh(ctx context.Context, rawToken string) (*AuthResponse, error) {
	tokenHash := hashToken(rawToken)

	rt, err := s.tokens.FindByHash(ctx, tokenHash)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}

	// A revoked refresh token presented for refresh is a compromise
	// signal: the token was already rotated once, and now an attacker
	// (or — rarer — a buggy client) is trying to reuse it. Burn every
	// outstanding refresh token for this user so the legitimate session
	// has to re-authenticate. The legitimate user will see one annoying
	// re-login; the attacker loses all tokens they may have stolen.
	if rt.RevokedAt != nil {
		_ = s.tokens.RevokeAllForUser(ctx, rt.UserID)
		return nil, ErrTokenRevoked
	}
	if time.Now().After(rt.ExpiresAt) {
		return nil, ErrTokenExpired
	}

	if err := s.tokens.Revoke(ctx, tokenHash); err != nil {
		return nil, err
	}

	user, err := s.users.FindByID(ctx, rt.UserID)
	if err != nil {
		return nil, err
	}
	if !user.IsActive {
		return nil, ErrAccountInactive
	}

	return s.issueTokens(ctx, user)
}

func (s *AuthService) Logout(ctx context.Context, userID, jti uuid.UUID, accessExpiresAt time.Time) error {
	if err := s.tokens.RevokeAllForUser(ctx, userID); err != nil {
		return err
	}
	return s.denylist.Add(ctx, jti, accessExpiresAt)
}

func (s *AuthService) Me(ctx context.Context, userID uuid.UUID) (*models.User, error) {
	return s.users.FindByID(ctx, userID)
}

// ─── User self-service ────────────────────────────────────────────────────────

func (s *AuthService) UpdateProfile(ctx context.Context, userID uuid.UUID, displayName, email string) (*models.User, error) {
	current, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if email != current.Email {
		if exists, err := s.users.ExistsByEmail(ctx, email); err != nil {
			return nil, err
		} else if exists {
			return nil, fmt.Errorf("email %w", repository.ErrDuplicate)
		}
	}
	return s.users.UpdateProfile(ctx, userID, displayName, email)
}

func (s *AuthService) UpdatePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword string) error {
	hash, err := s.identities.GetLocalPasswordHash(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrInvalidCredentials
	}
	if err != nil {
		return err
	}
	if err := auth.VerifyPassword(hash, currentPassword); err != nil {
		return ErrInvalidCredentials
	}
	newHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.identities.UpdateLocalPassword(ctx, userID, newHash)
}

// ─── Admin user management ────────────────────────────────────────────────────

var (
	ErrSelfDelete     = errors.New("cannot delete your own account")
	ErrSelfDeactivate = errors.New("cannot deactivate your own account")
	ErrSelfDemote     = errors.New("cannot remove your own admin privileges")
)

// UserPatch holds optional fields for an admin PATCH operation.
// A nil pointer means "leave unchanged".
type UserPatch struct {
	DisplayName     *string
	Email           *string
	IsActive        *bool
	IsInstanceAdmin *bool
}

func (s *AuthService) SearchUsers(ctx context.Context, query string) ([]*models.User, error) {
	return s.users.Search(ctx, query, 10)
}

func (s *AuthService) ListUsers(ctx context.Context, page, perPage int) ([]*models.User, int, error) {
	if perPage <= 0 || perPage > 100 {
		perPage = 20
	}
	if page <= 0 {
		page = 1
	}
	return s.users.List(ctx, perPage, (page-1)*perPage)
}

// AdminCreateUser creates an account directly, bypassing the RegistrationEnabled flag.
// Returns the created user (no tokens — the new user logs in themselves).
func (s *AuthService) AdminCreateUser(ctx context.Context, req RegisterRequest) (*models.User, error) {
	if exists, err := s.users.ExistsByUsername(ctx, req.Username); err != nil {
		return nil, err
	} else if exists {
		return nil, fmt.Errorf("username %w", repository.ErrDuplicate)
	}
	if exists, err := s.users.ExistsByEmail(ctx, req.Email); err != nil {
		return nil, err
	} else if exists {
		return nil, fmt.Errorf("email %w", repository.ErrDuplicate)
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	userID := uuid.New()
	identityID := uuid.New()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	user, err := s.users.Create(ctx, tx, userID, req.Username, req.Email, req.DisplayName, false)
	if err != nil {
		return nil, err
	}
	if err := s.identities.CreateLocal(ctx, tx, identityID, userID, req.Username, hash); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}
	return user, nil
}

// AdminPatchUser applies a partial update to any user. The caller fetches the
// current record; only non-nil patch fields overwrite it.
// callerID is used to prevent self-lockout.
func (s *AuthService) AdminPatchUser(ctx context.Context, targetID, callerID uuid.UUID, patch UserPatch) (*models.User, error) {
	if targetID == callerID {
		if patch.IsActive != nil && !*patch.IsActive {
			return nil, ErrSelfDeactivate
		}
		if patch.IsInstanceAdmin != nil && !*patch.IsInstanceAdmin {
			return nil, ErrSelfDemote
		}
	}
	current, err := s.users.FindByID(ctx, targetID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	// Check email uniqueness only when it changes
	newEmail := current.Email
	if patch.Email != nil && *patch.Email != current.Email {
		if exists, err := s.users.ExistsByEmail(ctx, *patch.Email); err != nil {
			return nil, err
		} else if exists {
			return nil, fmt.Errorf("email %w", repository.ErrDuplicate)
		}
		newEmail = *patch.Email
	}

	displayName := current.DisplayName
	if patch.DisplayName != nil {
		displayName = *patch.DisplayName
	}
	isActive := current.IsActive
	if patch.IsActive != nil {
		isActive = *patch.IsActive
	}
	isAdmin := current.IsInstanceAdmin
	if patch.IsInstanceAdmin != nil {
		isAdmin = *patch.IsInstanceAdmin
	}

	return s.users.AdminUpdate(ctx, targetID, displayName, newEmail, isActive, isAdmin)
}

func (s *AuthService) AdminDeleteUser(ctx context.Context, targetID, callerID uuid.UUID) error {
	if targetID == callerID {
		return ErrSelfDelete
	}
	return s.users.Delete(ctx, targetID)
}

func (s *AuthService) issueTokens(ctx context.Context, user *models.User) (*AuthResponse, error) {
	accessToken, err := s.jwt.Generate(user.ID, user.IsInstanceAdmin)
	if err != nil {
		return nil, err
	}

	rawRefresh, tokenHash, err := generateRefreshToken()
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(s.cfg.RefreshTTL)
	if err := s.tokens.Create(ctx, uuid.New(), user.ID, tokenHash, expiresAt); err != nil {
		return nil, err
	}

	return &AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    int(s.cfg.AccessTTL.Seconds()),
		User:         user,
	}, nil
}

func generateRefreshToken() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generating refresh token: %w", err)
	}
	raw = hex.EncodeToString(b)
	hash = hashToken(raw)
	return raw, hash, nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
