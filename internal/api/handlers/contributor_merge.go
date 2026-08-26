// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

// Folding duplicate contributors together.
//
// Instance admin only, and not because merging is dangerous to perform.
// contributors carry no library_id: they are instance-wide the way genres are,
// so one household tidying a name changes what every other household on the
// server sees. That is the same shape as the rest of the shared vocabulary,
// and it is administered in the same place.

type ContributorMergeHandler struct {
	contributors *repository.ContributorRepo
}

func NewContributorMergeHandler(c *repository.ContributorRepo) *ContributorMergeHandler {
	return &ContributorMergeHandler{contributors: c}
}

// ListDuplicates godoc
//
// @Summary     Contributors the catalogue believes are several people
// @Description Groups of names that differ only by punctuation, or by spacing between initials. Pairs an admin has already rejected are left out. Detection is an inference rather than a fact, so nothing is merged without a person saying so.
// @Tags        admin
// @Produce     json
// @Security    BearerAuth
// @Success     200  {object}  object{items=[]repository.DuplicateCandidate,total=int}
// @Failure     403  {object}  object{error=string}
// @Router      /admin/contributor-duplicates [get]
func (h *ContributorMergeHandler) ListDuplicates(w http.ResponseWriter, r *http.Request) {
	groups, err := h.contributors.DuplicateCandidates(r.Context())
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": groups, "total": len(groups)})
}

type mergeBody struct {
	SurvivorID string   `json:"survivor_id"`
	LoserIDs   []string `json:"loser_ids"`
}

// MergeContributors godoc
//
// @Summary     Fold contributors into one
// @Description The losers become tombstones rather than being deleted: their credits repoint at the survivor and merged_into records where they went, so a wrong merge is undone by clearing one column and an id a client cached still resolves to a real person.
// @Tags        admin
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body  object{survivor_id=string,loser_ids=[]string}  true  "Who survives, and who folds into them"
// @Success     200  {object}  object{credits=int,collapsed=int,merged=int}
// @Failure     400  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Router      /admin/contributor-duplicates/merge [post]
func (h *ContributorMergeHandler) MergeContributors(w http.ResponseWriter, r *http.Request) {
	var body mergeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	survivor, err := uuid.Parse(body.SurvivorID)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid survivor id")
		return
	}
	losers, err := parseIDs(body.LoserIDs)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid contributor id")
		return
	}
	if len(losers) == 0 {
		respond.Error(w, http.StatusBadRequest, "nothing to merge")
		return
	}

	res, err := h.contributors.Merge(r.Context(), survivor, losers)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, res)
}

type dismissBody struct {
	IDs []string `json:"ids"`
}

// DismissDuplicates godoc
//
// @Summary     Record that these contributors are different people
// @Description Every pair in the group, so a group of three that is really three people is settled once rather than coming back as three pairs on the next sweep.
// @Tags        admin
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body  body  object{ids=[]string}  true  "The contributors that are not the same person"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     403  {object}  object{error=string}
// @Router      /admin/contributor-duplicates/dismiss [post]
func (h *ContributorMergeHandler) DismissDuplicates(w http.ResponseWriter, r *http.Request) {
	var body dismissBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ids, err := parseIDs(body.IDs)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid contributor id")
		return
	}
	if len(ids) < 2 {
		respond.Error(w, http.StatusBadRequest, "a dismissal needs at least two contributors")
		return
	}

	var by uuid.UUID
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		by = claims.UserID
	}
	if err := h.contributors.Dismiss(r.Context(), ids, by); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseIDs(raw []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}
