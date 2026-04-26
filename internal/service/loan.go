// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"fmt"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

type LoanService struct {
	loans *repository.LoanRepo
	tags  *repository.TagRepo
}

func NewLoanService(loans *repository.LoanRepo, tags *repository.TagRepo) *LoanService {
	return &LoanService{loans: loans, tags: tags}
}

type LoanRequest struct {
	BookID   uuid.UUID
	LoanedTo string
	LoanedAt time.Time
	DueDate  *time.Time
	Notes    string
	TagIDs   []uuid.UUID
}

type LoanUpdateRequest struct {
	LoanedTo   string
	DueDate    *time.Time
	ReturnedAt *time.Time
	Notes      string
	TagIDs     []uuid.UUID
}

func (s *LoanService) ListLoans(ctx context.Context, libraryID uuid.UUID, includeReturned bool, search, tagFilter string, bookID uuid.UUID) ([]*models.Loan, error) {
	return s.loans.List(ctx, libraryID, includeReturned, search, tagFilter, bookID)
}

func (s *LoanService) CreateLoan(ctx context.Context, libraryID, callerID uuid.UUID, req LoanRequest) (*models.Loan, error) {
	if req.LoanedTo == "" {
		return nil, fmt.Errorf("loaned_to is required")
	}
	loan, err := s.loans.Create(ctx, uuid.New(), libraryID, req.BookID, callerID,
		req.LoanedTo, req.Notes, req.LoanedAt, req.DueDate)
	if err != nil {
		return nil, err
	}
	if req.TagIDs != nil {
		if err := s.tags.SetLoanTags(ctx, loan.ID, req.TagIDs); err != nil {
			return nil, fmt.Errorf("setting loan tags: %w", err)
		}
		loan, err = s.loans.FindByID(ctx, loan.ID)
		if err != nil {
			return nil, err
		}
	}
	return loan, nil
}

func (s *LoanService) UpdateLoan(ctx context.Context, id uuid.UUID, req LoanUpdateRequest) (*models.Loan, error) {
	if req.LoanedTo == "" {
		return nil, fmt.Errorf("loaned_to is required")
	}
	loan, err := s.loans.Update(ctx, id, req.LoanedTo, req.Notes, req.DueDate, req.ReturnedAt)
	if err != nil {
		return nil, err
	}
	if req.TagIDs != nil {
		if err := s.tags.SetLoanTags(ctx, loan.ID, req.TagIDs); err != nil {
			return nil, fmt.Errorf("setting loan tags: %w", err)
		}
		loan, err = s.loans.FindByID(ctx, loan.ID)
		if err != nil {
			return nil, err
		}
	}
	return loan, nil
}

func (s *LoanService) DeleteLoan(ctx context.Context, id uuid.UUID) error {
	return s.loans.Delete(ctx, id)
}
