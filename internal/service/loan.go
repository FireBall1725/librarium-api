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
}

func NewLoanService(loans *repository.LoanRepo) *LoanService {
	return &LoanService{loans: loans}
}

type LoanRequest struct {
	BookID   uuid.UUID
	LoanedTo string
	LoanedAt time.Time
	DueDate  *time.Time
	Notes    string
}

type LoanUpdateRequest struct {
	LoanedTo   string
	DueDate    *time.Time
	ReturnedAt *time.Time
	Notes      string
}

func (s *LoanService) ListLoans(ctx context.Context, libraryID uuid.UUID, includeReturned bool, search string, bookID uuid.UUID) ([]*models.Loan, error) {
	return s.loans.List(ctx, libraryID, includeReturned, search, bookID)
}

func (s *LoanService) CreateLoan(ctx context.Context, libraryID, callerID uuid.UUID, req LoanRequest) (*models.Loan, error) {
	if req.LoanedTo == "" {
		return nil, fmt.Errorf("loaned_to is required")
	}
	return s.loans.Create(ctx, uuid.New(), libraryID, req.BookID, callerID,
		req.LoanedTo, req.Notes, req.LoanedAt, req.DueDate)
}

func (s *LoanService) UpdateLoan(ctx context.Context, id uuid.UUID, req LoanUpdateRequest) (*models.Loan, error) {
	if req.LoanedTo == "" {
		return nil, fmt.Errorf("loaned_to is required")
	}
	return s.loans.Update(ctx, id, req.LoanedTo, req.Notes, req.DueDate, req.ReturnedAt)
}

func (s *LoanService) DeleteLoan(ctx context.Context, id uuid.UUID) error {
	return s.loans.Delete(ctx, id)
}
