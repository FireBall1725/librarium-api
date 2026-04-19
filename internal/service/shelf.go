// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"fmt"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

type ShelfService struct {
	shelves *repository.ShelfRepo
	tags    *repository.TagRepo
}

func NewShelfService(shelves *repository.ShelfRepo, tags *repository.TagRepo) *ShelfService {
	return &ShelfService{shelves: shelves, tags: tags}
}

// ─── Shelves ──────────────────────────────────────────────────────────────────

func (s *ShelfService) ListShelves(ctx context.Context, libraryID uuid.UUID, search, tagFilter string) ([]*models.Shelf, error) {
	return s.shelves.List(ctx, libraryID, search, tagFilter)
}

func (s *ShelfService) CreateShelf(ctx context.Context, libraryID, callerID uuid.UUID, name, description, color, icon string, displayOrder int, tagIDs []uuid.UUID) (*models.Shelf, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	shelf, err := s.shelves.Create(ctx, uuid.New(), libraryID, name, description, color, icon, displayOrder, callerID)
	if err != nil {
		return nil, err
	}
	if tagIDs != nil {
		if err := s.tags.SetShelfTags(ctx, shelf.ID, tagIDs); err != nil {
			return nil, fmt.Errorf("setting shelf tags: %w", err)
		}
		shelf, err = s.shelves.FindByID(ctx, shelf.ID)
		if err != nil {
			return nil, err
		}
	}
	return shelf, nil
}

func (s *ShelfService) UpdateShelf(ctx context.Context, id uuid.UUID, name, description, color, icon string, displayOrder int, tagIDs []uuid.UUID) (*models.Shelf, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	shelf, err := s.shelves.Update(ctx, id, name, description, color, icon, displayOrder)
	if err != nil {
		return nil, err
	}
	if tagIDs != nil {
		if err := s.tags.SetShelfTags(ctx, shelf.ID, tagIDs); err != nil {
			return nil, fmt.Errorf("setting shelf tags: %w", err)
		}
		shelf, err = s.shelves.FindByID(ctx, shelf.ID)
		if err != nil {
			return nil, err
		}
	}
	return shelf, nil
}

func (s *ShelfService) DeleteShelf(ctx context.Context, id uuid.UUID) error {
	return s.shelves.Delete(ctx, id)
}

func (s *ShelfService) ListBookShelves(ctx context.Context, libraryID, bookID uuid.UUID) ([]*models.Shelf, error) {
	return s.shelves.FindByBook(ctx, libraryID, bookID)
}

func (s *ShelfService) ListShelfBooks(ctx context.Context, shelfID uuid.UUID) ([]*models.Book, error) {
	return s.shelves.ListBooks(ctx, shelfID)
}

func (s *ShelfService) AddBookToShelf(ctx context.Context, shelfID, bookID, callerID uuid.UUID) error {
	return s.shelves.AddBook(ctx, shelfID, bookID, callerID)
}

func (s *ShelfService) RemoveBookFromShelf(ctx context.Context, shelfID, bookID uuid.UUID) error {
	return s.shelves.RemoveBook(ctx, shelfID, bookID)
}

// ─── Tags ─────────────────────────────────────────────────────────────────────

func (s *ShelfService) ListTags(ctx context.Context, libraryID uuid.UUID) ([]*models.Tag, error) {
	return s.tags.List(ctx, libraryID)
}

func (s *ShelfService) CreateTag(ctx context.Context, libraryID, callerID uuid.UUID, name, color string) (*models.Tag, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	return s.tags.Create(ctx, uuid.New(), libraryID, name, color, callerID)
}

func (s *ShelfService) UpdateTag(ctx context.Context, id uuid.UUID, name, color string) (*models.Tag, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	return s.tags.Update(ctx, id, name, color)
}

func (s *ShelfService) DeleteTag(ctx context.Context, id uuid.UUID) error {
	return s.tags.Delete(ctx, id)
}
