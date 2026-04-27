// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package models

import (
	"time"

	"github.com/google/uuid"
)

type Loan struct {
	ID         uuid.UUID  `json:"id"`
	LibraryID  uuid.UUID  `json:"library_id"`
	BookID     uuid.UUID  `json:"book_id"`
	BookTitle  string     `json:"book_title"`
	LoanedTo   string     `json:"loaned_to"`
	LoanedAt   time.Time  `json:"loaned_at"`
	DueDate    *time.Time `json:"due_date"`
	ReturnedAt *time.Time `json:"returned_at"`
	Notes      string     `json:"notes"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
