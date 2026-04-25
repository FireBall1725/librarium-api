// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/fireball1725/librarium-api/internal/api/middleware"
	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/fireball1725/librarium-api/internal/api/uploads"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/fireball1725/librarium-api/internal/service"
	"github.com/google/uuid"
)

// ServeBookCover godoc
//
// @Summary     Serve book cover image
// @Description Streams the primary cover image for a book.
// @Tags        covers
// @Produce     image/jpeg
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       book_id     path  string  true  "Book UUID"
// @Success     200
// @Failure     404
// @Router      /libraries/{library_id}/books/{book_id}/cover [get]
func (h *BookHandler) ServeBookCover(w http.ResponseWriter, r *http.Request) {
	bookID, err := uuid.Parse(r.PathValue("book_id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	filePath, mimeType, err := h.svc.GetBookCoverPath(r.Context(), bookID)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Type", mimeType)
	http.ServeFile(w, r, filePath)
}

// FetchBookCover godoc
//
// @Summary     Fetch book cover from URL
// @Description Downloads a cover image from the given URL and stores it as the book's cover.
// @Tags        covers
// @Accept      json
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       book_id     path  string  true  "Book UUID"
// @Param       body        body  object{url=string}  true  "Cover URL"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Failure     502  {object}  object{error=string}
// @Router      /libraries/{library_id}/books/{book_id}/cover/fetch [post]
func (h *BookHandler) FetchBookCover(w http.ResponseWriter, r *http.Request) {
	bookID, err := uuid.Parse(r.PathValue("book_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())

	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.URL == "" {
		respond.Error(w, http.StatusBadRequest, "url is required")
		return
	}

	if err := h.svc.FetchCoverFromURL(r.Context(), bookID, claims.UserID, body.URL); err != nil {
		var upstream service.ErrUpstreamHTTP
		if errors.As(err, &upstream) {
			msg := fmt.Sprintf("cover provider returned %d", upstream.StatusCode)
			if upstream.StatusCode == http.StatusTooManyRequests {
				msg = "cover provider is rate-limiting requests, try again later"
			}
			respond.Error(w, http.StatusBadGateway, msg)
			return
		}
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UploadBookCover godoc
//
// @Summary     Upload book cover
// @Description Accepts a multipart form upload and stores the image as the book's cover.
// @Tags        covers
// @Accept      multipart/form-data
// @Security    BearerAuth
// @Param       library_id  path      string  true  "Library UUID"
// @Param       book_id     path      string  true  "Book UUID"
// @Param       cover       formData  file    true  "Cover image file"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/books/{book_id}/cover [put]
func (h *BookHandler) UploadBookCover(w http.ResponseWriter, r *http.Request) {
	bookID, err := uuid.Parse(r.PathValue("book_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, _, err := r.FormFile("cover")
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "cover file is required")
		return
	}
	defer file.Close()

	// Detect MIME from the actual content rather than trusting the
	// client-supplied multipart header. SniffImage rejects anything outside
	// the cover allowlist (PNG / JPEG / GIF / WebP).
	mime, head, err := uploads.SniffImage(file)
	if errors.Is(err, uploads.ErrUnsupportedType) {
		respond.Error(w, http.StatusBadRequest, "file must be a PNG, JPEG, GIF, or WebP image")
		return
	}
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}

	// Stitch the bytes consumed during sniffing back onto the front of the
	// stream so the persisted file is byte-identical to what was uploaded.
	rest, err := io.ReadAll(io.LimitReader(file, (10<<20)-int64(len(head))))
	if err != nil {
		respond.ServerError(w, r, err)
		return
	}
	data := append(head, rest...)

	if err := h.svc.StoreCoverFromUpload(r.Context(), bookID, claims.UserID, data, mime); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteBookCover godoc
//
// @Summary     Delete book cover
// @Description Removes the cover image from a book.
// @Tags        covers
// @Security    BearerAuth
// @Param       library_id  path  string  true  "Library UUID"
// @Param       book_id     path  string  true  "Book UUID"
// @Success     204
// @Failure     400  {object}  object{error=string}
// @Failure     401  {object}  object{error=string}
// @Router      /libraries/{library_id}/books/{book_id}/cover [delete]
func (h *BookHandler) DeleteBookCover(w http.ResponseWriter, r *http.Request) {
	bookID, err := uuid.Parse(r.PathValue("book_id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid book id")
		return
	}
	if err := h.svc.DeleteBookCover(r.Context(), bookID); err != nil {
		respond.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
