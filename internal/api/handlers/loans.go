// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/google/uuid"
)

type LoanHandler struct {
	svc *service.LoanService
}

func NewLoanHandler(svc *service.LoanService) *LoanHandler {
	return &LoanHandler{svc: svc}
}

// ListLoans godoc
//
// @Summary     List loans in a library
// @Description Returns current (and optionally returned) loans for a library.
// @Tags        loans
// @Produce     json
// @Security    BearerAuth
// @Param       library_id        path      string   true   "Library UUID"
// @Param       include_returned  query     boolean  false  "Include returned loans"
// @Param       search            query     string   false  "Filter by borrower name"
// @Param       tag               query     string   false  "Filter by tag"
// @Param       book_id           query     string   false  "Filter to loans of a specific book"
// @Success     200  {array}   responses.LoanResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/loans [get]
func (h *LoanHandler) ListLoans(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	includeReturned := r.URL.Query().Get("include_returned") == "true"
	search := r.URL.Query().Get("search")
	tagFilter := r.URL.Query().Get("tag")
	var bookID uuid.UUID
	if raw := r.URL.Query().Get("book_id"); raw != "" {
		bookID, err = uuid.Parse(raw)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid book_id")
			return
		}
	}
	loans, err := h.svc.ListLoans(r.Context(), libraryID, includeReturned, search, tagFilter, bookID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(loans))
	for _, l := range loans {
		out = append(out, loanBody(l))
	}
	respond.JSON(w, http.StatusOK, out)
}

// CreateLoan godoc
//
// @Summary     Create a loan
// @Description Records that a book has been loaned to someone.
// @Tags        loans
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       body        body      object{book_id=string,loaned_to=string,loaned_at=string,due_date=string,notes=string,tag_ids=[]string}  true  "Loan details"
// @Success     201  {object}  responses.LoanResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/loans [post]
func (h *LoanHandler) CreateLoan(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	req, err := decodeLoanCreateRequest(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	loan, err := h.svc.CreateLoan(r.Context(), libraryID, claims.UserID, *req)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, loanBody(loan))
}

// UpdateLoan godoc
//
// @Summary     Update a loan
// @Description Updates loan details including marking a book as returned.
// @Tags        loans
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       loan_id     path      string  true  "Loan UUID"
// @Param       body        body      object{loaned_to=string,due_date=string,returned_at=string,notes=string,tag_ids=[]string}  true  "Updated loan"
// @Success     200  {object}  responses.LoanResponse
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/loans/{loan_id} [patch]
func (h *LoanHandler) UpdateLoan(w http.ResponseWriter, r *http.Request) {
	loanID, err := uuid.Parse(r.PathValue("loan_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid loan id")
		return
	}
	req, err := decodeLoanUpdateRequest(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	loan, err := h.svc.UpdateLoan(r.Context(), loanID, *req)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "loan not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, loanBody(loan))
}

// DeleteLoan godoc
//
// @Summary     Delete a loan
// @Description Permanently deletes a loan record.
// @Tags        loans
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       loan_id     path  string  true  "Loan UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Router      /libraries/{library_id}/loans/{loan_id} [delete]
func (h *LoanHandler) DeleteLoan(w http.ResponseWriter, r *http.Request) {
	loanID, err := uuid.Parse(r.PathValue("loan_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid loan id")
		return
	}
	if err := h.svc.DeleteLoan(r.Context(), loanID); errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "loan not found")
		return
	} else if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func decodeLoanCreateRequest(r *http.Request) (*service.LoanRequest, error) {
	var body struct {
		BookID   string   `json:"book_id"`
		LoanedTo string   `json:"loaned_to"`
		LoanedAt string   `json:"loaned_at"` // YYYY-MM-DD; defaults to today
		DueDate  string   `json:"due_date"`  // YYYY-MM-DD or ""
		Notes    string   `json:"notes"`
		TagIDs   []string `json:"tag_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, errors.New("invalid request body")
	}
	bookID, err := uuid.Parse(body.BookID)
	if err != nil {
		return nil, errors.New("invalid book_id")
	}
	if body.LoanedTo == "" {
		return nil, errors.New("loaned_to is required")
	}

	loanedAt := time.Now()
	if body.LoanedAt != "" {
		if t, err := time.Parse("2006-01-02", body.LoanedAt); err == nil {
			loanedAt = t
		}
	}

	var dueDate *time.Time
	if body.DueDate != "" {
		if t, err := time.Parse("2006-01-02", body.DueDate); err == nil {
			dueDate = &t
		}
	}

	var tagIDs []uuid.UUID
	if body.TagIDs != nil {
		tagIDs = make([]uuid.UUID, 0, len(body.TagIDs))
		for _, s := range body.TagIDs {
			id, err := uuid.Parse(s)
			if err != nil {
				return nil, errors.New("invalid tag_id: " + s)
			}
			tagIDs = append(tagIDs, id)
		}
	}

	return &service.LoanRequest{
		BookID:   bookID,
		LoanedTo: body.LoanedTo,
		LoanedAt: loanedAt,
		DueDate:  dueDate,
		Notes:    body.Notes,
		TagIDs:   tagIDs,
	}, nil
}

func decodeLoanUpdateRequest(r *http.Request) (*service.LoanUpdateRequest, error) {
	var body struct {
		LoanedTo   string   `json:"loaned_to"`
		DueDate    *string  `json:"due_date"`    // null = clear, "YYYY-MM-DD" = set
		ReturnedAt *string  `json:"returned_at"` // null = clear, "YYYY-MM-DD" = set
		Notes      string   `json:"notes"`
		TagIDs     []string `json:"tag_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, errors.New("invalid request body")
	}
	if body.LoanedTo == "" {
		return nil, errors.New("loaned_to is required")
	}

	parseDate := func(s *string) *time.Time {
		if s == nil || *s == "" {
			return nil
		}
		t, err := time.Parse("2006-01-02", *s)
		if err != nil {
			return nil
		}
		return &t
	}

	var tagIDs []uuid.UUID
	if body.TagIDs != nil {
		tagIDs = make([]uuid.UUID, 0, len(body.TagIDs))
		for _, s := range body.TagIDs {
			id, err := uuid.Parse(s)
			if err != nil {
				return nil, errors.New("invalid tag_id: " + s)
			}
			tagIDs = append(tagIDs, id)
		}
	}

	return &service.LoanUpdateRequest{
		LoanedTo:   body.LoanedTo,
		DueDate:    parseDate(body.DueDate),
		ReturnedAt: parseDate(body.ReturnedAt),
		Notes:      body.Notes,
		TagIDs:     tagIDs,
	}, nil
}

func tagsToBodyLoans(tags []*models.Tag) []map[string]any {
	out := make([]map[string]any, 0, len(tags))
	for _, t := range tags {
		out = append(out, map[string]any{"id": t.ID, "name": t.Name, "color": t.Color})
	}
	return out
}

// loanBodies projects a slice of loans through loanBody. Always returns a
// non-nil slice so JSON marshalling yields [] not null.
func loanBodies(loans []*models.Loan) []map[string]any {
	out := make([]map[string]any, 0, len(loans))
	for _, l := range loans {
		out = append(out, loanBody(l))
	}
	return out
}

func loanBody(l *models.Loan) map[string]any {
	tags := l.Tags
	if tags == nil {
		tags = []*models.Tag{}
	}
	body := map[string]any{
		"id":         l.ID,
		"library_id": l.LibraryID,
		"book_id":    l.BookID,
		"book_title": l.BookTitle,
		"loaned_to":  l.LoanedTo,
		"loaned_at":  l.LoanedAt.Format("2006-01-02"),
		"notes":      l.Notes,
		"tags":       tagsToBodyLoans(tags),
		"created_at": l.CreatedAt,
		"updated_at": l.UpdatedAt,
	}
	if l.DueDate != nil {
		body["due_date"] = l.DueDate.Format("2006-01-02")
	} else {
		body["due_date"] = nil
	}
	if l.ReturnedAt != nil {
		body["returned_at"] = l.ReturnedAt.Format("2006-01-02")
	} else {
		body["returned_at"] = nil
	}
	return body
}
