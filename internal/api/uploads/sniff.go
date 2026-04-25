// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

// Package uploads centralizes content-type detection for multipart file
// uploads. The previous handlers trusted the client-supplied Content-Type
// from the multipart header, which is trivially spoofed — a user could
// upload an executable advertised as `image/png` and bypass the check.
//
// SniffImage / SniffEditionFile read the leading bytes of the actual upload,
// run them through the standard library's content-type sniffer, and check
// against an allowlist. Callers should use the returned MIME instead of any
// client-provided value.
package uploads

import (
	"errors"
	"io"
	"net/http"
)

// ErrUnsupportedType is returned when an upload's actual content does not
// match the allowlist for that endpoint.
var ErrUnsupportedType = errors.New("unsupported file type")

// allowedImageTypes is the closed set of cover / contributor-photo MIME
// types we accept. SVG is intentionally excluded — it can carry script.
var allowedImageTypes = map[string]struct{}{
	"image/png":  {},
	"image/jpeg": {},
	"image/gif":  {},
	"image/webp": {},
}

// allowedEditionFileTypes is the closed set of ebook / audiobook MIME types
// we accept on the edition-file upload endpoint. Generic application/zip is
// allowed because EPUB/CBZ files are detected as zip by net/http; the
// service layer pins format more narrowly via the explicit `format` field.
var allowedEditionFileTypes = map[string]struct{}{
	"application/pdf":  {},
	"application/epub+zip": {},
	"application/zip":  {},
	"audio/mpeg":       {},
	"audio/mp4":        {},
	"audio/m4a":        {},
	"audio/x-m4a":      {},
	"audio/x-m4b":      {},
	"audio/aac":        {},
	"audio/ogg":        {},
	"audio/flac":       {},
	"audio/x-flac":     {},
	"audio/wav":        {},
	"audio/x-wav":      {},
}

// SniffImage validates that `r` is one of the allowed image types and
// returns the canonical MIME plus the bytes it consumed during sniffing
// (callers must use the returned `head`, then read the rest from `r`).
func SniffImage(r io.Reader) (mime string, head []byte, err error) {
	return sniff(r, allowedImageTypes)
}

// SniffEditionFile validates that `r` is one of the allowed ebook /
// audiobook types and returns the canonical MIME plus consumed bytes.
func SniffEditionFile(r io.Reader) (mime string, head []byte, err error) {
	return sniff(r, allowedEditionFileTypes)
}

// sniff reads up to 512 bytes (net/http's documented sniff window),
// classifies the content, and rejects anything outside the allowlist.
// The consumed bytes are returned so callers can prepend them to the rest
// of the stream when persisting.
func sniff(r io.Reader, allowed map[string]struct{}) (string, []byte, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", nil, err
	}
	head = head[:n]
	detected := http.DetectContentType(head)
	// Strip parameters (e.g. "image/jpeg; charset=binary").
	for i, c := range detected {
		if c == ';' {
			detected = detected[:i]
			break
		}
	}
	if _, ok := allowed[detected]; !ok {
		return "", nil, ErrUnsupportedType
	}
	return detected, head, nil
}
