// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"errors"
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/providers"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/google/uuid"
)

// SourcesHandler serves the answers each provider gave for an edition.
type SourcesHandler struct {
	editions  *repository.EditionRepo
	answers   *repository.EditionAnswerRepo
	providers *service.ProviderService
}

func NewSourcesHandler(editions *repository.EditionRepo, answers *repository.EditionAnswerRepo, providers *service.ProviderService) *SourcesHandler {
	return &SourcesHandler{editions: editions, answers: answers, providers: providers}
}

type sourcesBody struct {
	// HasAnswers is false for editions added before answers were kept, or
	// added by hand; the client offers to ask the providers.
	HasAnswers bool                        `json:"has_answers"`
	Answers    []*repository.EditionAnswer `json:"answers"`
	Merged     *providers.MergedBookResult `json:"merged"`
}

// GetEditionSources godoc
//
// @Summary     What each provider said about a printing
// @Description Every stored provider answer for the edition, and the same merged view a lookup returns, built from those answers without asking any provider. Switching a field to another answer is a normal edition update.
// @Tags        catalogue
// @Produce     json
// @Security    BearerAuth
// @Param       edition_id  path  string  true  "Edition UUID"
// @Success     200  {object}  object{has_answers=boolean,answers=[]object{provider=string,lookup_key=string,fetched_at=string,result=providers.BookResult},merged=providers.MergedBookResult}
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /editions/{edition_id}/sources [get]
func (h *SourcesHandler) GetEditionSources(w http.ResponseWriter, r *http.Request) {
	editionID, err := uuid.Parse(r.PathValue("edition_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid edition id")
		return
	}
	body, err := h.sources(r, editionID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, body)
}

// AskEditionSource godoc
//
// @Summary     Ask one provider about a printing now
// @Description Looks the edition's ISBN up with one provider and stores its answer next to the others, replacing that provider's earlier one. For a provider turned on after the book was added. found is false when the provider has no record.
// @Tags        catalogue
// @Produce     json
// @Security    BearerAuth
// @Param       edition_id  path  string  true  "Edition UUID"
// @Param       provider    path  string  true  "Provider name, e.g. open_library"
// @Success     200  {object}  object{found=boolean,has_answers=boolean,answers=[]object{provider=string,lookup_key=string,fetched_at=string,result=providers.BookResult},merged=providers.MergedBookResult}
// @Failure     400  {object}  object{error=string}
// @Failure     404  {object}  object{error=string}
// @Failure     502  {object}  object{error=string}
// @Router      /editions/{edition_id}/sources/{provider} [post]
func (h *SourcesHandler) AskEditionSource(w http.ResponseWriter, r *http.Request) {
	editionID, err := uuid.Parse(r.PathValue("edition_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid edition id")
		return
	}
	edition, err := h.editions.FindByID(r.Context(), editionID)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "edition not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	isbn := edition.ISBN13
	if isbn == "" {
		isbn = edition.ISBN10
	}
	if isbn == "" {
		respond.Error(w, http.StatusBadRequest, "this edition has no ISBN to look up")
		return
	}

	name := r.PathValue("provider")
	result, ok, err := h.providers.LookupOne(r.Context(), name, isbn)
	if !ok {
		respond.Error(w, http.StatusNotFound, "no enabled provider by that name")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusBadGateway, "the provider didn't answer: "+err.Error())
		return
	}
	if result != nil {
		if err := h.answers.Save(r.Context(), editionID, providers.BarcodeKey(isbn), []*providers.BookResult{result}); err != nil {
			respond.ServerError(w, r, err)
			return
		}
	}

	body, err := h.sources(r, editionID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"found":       result != nil,
		"has_answers": body.HasAnswers,
		"answers":     body.Answers,
		"merged":      body.Merged,
	})
}

func (h *SourcesHandler) sources(r *http.Request, editionID uuid.UUID) (*sourcesBody, error) {
	answers, err := h.answers.ListForEdition(r.Context(), editionID)
	if err != nil {
		return nil, err
	}
	results := make([]*providers.BookResult, 0, len(answers))
	for _, a := range answers {
		if a.Result != nil {
			results = append(results, a.Result)
		}
	}
	merged := providers.MergeBookResults(results)
	if merged.Categories == nil {
		merged.Categories = []string{}
	}
	if merged.Covers == nil {
		merged.Covers = []providers.CoverOption{}
	}
	return &sourcesBody{HasAnswers: len(answers) > 0, Answers: answers, Merged: merged}, nil
}
