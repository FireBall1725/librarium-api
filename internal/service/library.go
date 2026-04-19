// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrCannotRemoveOwner = errors.New("cannot remove or change the library owner")
	ErrNotLibraryMember  = errors.New("not a library member")
)

type LibraryService struct {
	pool        *pgxpool.Pool
	libraries   *repository.LibraryRepo
	memberships *repository.MembershipRepo
	roles       *repository.RoleRepo
	users       *repository.UserRepo
	shelves     *repository.ShelfRepo
}

func NewLibraryService(
	pool *pgxpool.Pool,
	libraries *repository.LibraryRepo,
	memberships *repository.MembershipRepo,
	roles *repository.RoleRepo,
	users *repository.UserRepo,
	shelves *repository.ShelfRepo,
) *LibraryService {
	return &LibraryService{
		pool:        pool,
		libraries:   libraries,
		memberships: memberships,
		roles:       roles,
		users:       users,
		shelves:     shelves,
	}
}

var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = nonAlphanumRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// ─── Library CRUD ─────────────────────────────────────────────────────────────

type CreateLibraryRequest struct {
	Name        string
	Description string
	Slug        string // optional; auto-derived from Name if empty
	IsPublic    bool
}

func (s *LibraryService) CreateLibrary(ctx context.Context, ownerID uuid.UUID, req CreateLibraryRequest) (*models.Library, error) {
	slug := req.Slug
	if slug == "" {
		slug = slugify(req.Name)
	}
	if slug == "" {
		return nil, fmt.Errorf("could not derive slug from name")
	}

	// Guarantee uniqueness by appending a counter if needed.
	base := slug
	for i := 2; ; i++ {
		exists, err := s.libraries.ExistsBySlug(ctx, slug)
		if err != nil {
			return nil, err
		}
		if !exists {
			break
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}

	ownerRoleID, err := s.roles.FindIDByName(ctx, "library_owner")
	if err != nil {
		return nil, fmt.Errorf("looking up library_owner role: %w", err)
	}

	libraryID := uuid.New()
	membershipID := uuid.New()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	lib, err := s.libraries.Create(ctx, tx, libraryID, req.Name, req.Description, slug, ownerID, req.IsPublic)
	if err != nil {
		return nil, err
	}

	if err := s.memberships.Add(ctx, tx, membershipID, libraryID, ownerID, ownerRoleID, nil); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	// Create default Favourites shelf (best-effort — library is already committed)
	if _, err := s.shelves.Create(ctx, uuid.New(), lib.ID, "Favourites", "", "", "⭐", 0, ownerID); err != nil {
		slog.Warn("failed to create default Favourites shelf", "library_id", lib.ID, "err", err)
	}

	return lib, nil
}

func (s *LibraryService) GetLibrary(ctx context.Context, id uuid.UUID) (*models.Library, error) {
	return s.libraries.FindByID(ctx, id)
}

func (s *LibraryService) ListLibraries(ctx context.Context, callerID uuid.UUID, isAdmin bool) ([]*models.Library, error) {
	if isAdmin {
		return s.libraries.ListAll(ctx)
	}
	return s.libraries.ListForUser(ctx, callerID)
}

type UpdateLibraryRequest struct {
	Name        string
	Description string
	IsPublic    bool
}

func (s *LibraryService) UpdateLibrary(ctx context.Context, id uuid.UUID, req UpdateLibraryRequest) (*models.Library, error) {
	return s.libraries.Update(ctx, id, req.Name, req.Description, req.IsPublic)
}

func (s *LibraryService) DeleteLibrary(ctx context.Context, id uuid.UUID) error {
	return s.libraries.Delete(ctx, id)
}

// ─── Members ──────────────────────────────────────────────────────────────────

func (s *LibraryService) ListMembers(ctx context.Context, libraryID uuid.UUID, search, tagFilter string) ([]*models.LibraryMember, error) {
	return s.memberships.ListByLibrary(ctx, libraryID, search, tagFilter)
}

func (s *LibraryService) AddMember(ctx context.Context, libraryID, targetUserID, invitedBy uuid.UUID, roleName string) error {
	if _, err := s.users.FindByID(ctx, targetUserID); errors.Is(err, repository.ErrNotFound) {
		return repository.ErrNotFound
	} else if err != nil {
		return err
	}

	roleID, err := s.roles.FindIDByName(ctx, roleName)
	if err != nil {
		return fmt.Errorf("looking up role: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := s.memberships.Add(ctx, tx, uuid.New(), libraryID, targetUserID, roleID, &invitedBy); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *LibraryService) UpdateMemberRole(ctx context.Context, libraryID, targetUserID uuid.UUID, roleName string) error {
	lib, err := s.libraries.FindByID(ctx, libraryID)
	if err != nil {
		return err
	}
	if lib.OwnerID == targetUserID {
		return ErrCannotRemoveOwner
	}

	roleID, err := s.roles.FindIDByName(ctx, roleName)
	if err != nil {
		return fmt.Errorf("looking up role: %w", err)
	}

	return s.memberships.UpdateRole(ctx, libraryID, targetUserID, roleID)
}

func (s *LibraryService) RemoveMember(ctx context.Context, libraryID, targetUserID uuid.UUID) error {
	lib, err := s.libraries.FindByID(ctx, libraryID)
	if err != nil {
		return err
	}
	if lib.OwnerID == targetUserID {
		return ErrCannotRemoveOwner
	}

	return s.memberships.Remove(ctx, libraryID, targetUserID)
}
