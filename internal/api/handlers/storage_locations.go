// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/google/uuid"
)

type StorageLocationHandler struct {
	svc *service.EditionFileService
}

func NewStorageLocationHandler(svc *service.EditionFileService) *StorageLocationHandler {
	return &StorageLocationHandler{svc: svc}
}

func storageLocationBody(loc *models.StorageLocation) map[string]any {
	return map[string]any{
		"id":            loc.ID,
		"library_id":    loc.LibraryID,
		"name":          loc.Name,
		"root_path":     loc.RootPath,
		"media_format":  loc.MediaFormat,
		"path_template": loc.PathTemplate,
		"created_at":    loc.CreatedAt,
		"updated_at":    loc.UpdatedAt,
	}
}

func (h *StorageLocationHandler) List(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	locs, err := h.svc.ListStorageLocations(r.Context(), libraryID)
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(locs))
	for _, loc := range locs {
		out = append(out, storageLocationBody(loc))
	}
	respond.JSON(w, http.StatusOK, out)
}

func (h *StorageLocationHandler) Create(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	var body struct {
		Name         string `json:"name"`
		RootPath     string `json:"root_path"`
		MediaFormat  string `json:"media_format"`
		PathTemplate string `json:"path_template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	loc, err := h.svc.CreateStorageLocation(r.Context(), libraryID, body.Name, body.RootPath, body.MediaFormat, body.PathTemplate)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusCreated, storageLocationBody(loc))
}

func (h *StorageLocationHandler) Update(w http.ResponseWriter, r *http.Request) {
	locationID, err := uuid.Parse(r.PathValue("location_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid location id")
		return
	}
	var body struct {
		Name         string `json:"name"`
		RootPath     string `json:"root_path"`
		MediaFormat  string `json:"media_format"`
		PathTemplate string `json:"path_template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	loc, err := h.svc.UpdateStorageLocation(r.Context(), locationID, body.Name, body.RootPath, body.MediaFormat, body.PathTemplate)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "storage location not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, storageLocationBody(loc))
}

func (h *StorageLocationHandler) Delete(w http.ResponseWriter, r *http.Request) {
	locationID, err := uuid.Parse(r.PathValue("location_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid location id")
		return
	}
	if err := h.svc.DeleteStorageLocation(r.Context(), locationID); errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "storage location not found")
		return
	} else if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *StorageLocationHandler) Scan(w http.ResponseWriter, r *http.Request) {
	libraryID, err := uuid.Parse(r.PathValue("library_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid library id")
		return
	}
	locationID, err := uuid.Parse(r.PathValue("location_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid location id")
		return
	}
	result, err := h.svc.ScanStorageLocation(r.Context(), libraryID, locationID)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "storage location not found")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, result)
}
