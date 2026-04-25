// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package uploads

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// pngHeader is the canonical 8-byte PNG signature followed by enough
// bytes to round out the sniff window without depending on a real PNG.
var pngHeader = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// jpegHeader / gifHeader / webpHeader / pdfHeader / zipHeader are
// minimal magic-byte prefixes that net/http.DetectContentType
// recognises. They mirror the formats SniffImage / SniffEditionFile
// accept so the tests don't reach for external fixture files.
var jpegHeader = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 16, 'J', 'F', 'I', 'F', 0}
var gifHeader = []byte("GIF89a")

// webpHeader needs the VP8L (or VP8 ) sub-chunk after the WEBP marker
// for http.DetectContentType to recognise it; an empty RIFF/WEBP
// envelope alone returns application/octet-stream.
var webpHeader = append([]byte("RIFF\x00\x00\x00\x00WEBPVP8L"), make([]byte, 32)...)
var pdfHeader = []byte("%PDF-1.4\n")
var zipHeader = []byte("PK\x03\x04")

// mp3Header uses an ID3v2 prefix because that's what real MP3 files
// almost always start with — bare frame headers (0xFF 0xFB ...) fall
// through DetectContentType to application/octet-stream.
var mp3Header = append([]byte("ID3\x03\x00\x00\x00\x00\x00\x00"), make([]byte, 64)...)

func TestSniffImage_AcceptsAllowedTypes(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"image/png":  pngHeader,
		"image/jpeg": jpegHeader,
		"image/gif":  gifHeader,
		"image/webp": webpHeader,
	}
	for wantMime, head := range cases {
		t.Run(wantMime, func(t *testing.T) {
			got, _, err := SniffImage(bytes.NewReader(head))
			if err != nil {
				t.Fatalf("SniffImage(%s): unexpected error %v", wantMime, err)
			}
			if got != wantMime {
				t.Errorf("SniffImage: got %q, want %q", got, wantMime)
			}
		})
	}
}

// TestSniffImage_RejectsSpoofedContentType is the load-bearing
// assertion: the previous handlers trusted the multipart Content-Type
// header, so a `Content-Type: image/png` upload of a PDF would slip
// through. SniffImage looks at the leading bytes; this guards against
// regressing back to header-trust.
func TestSniffImage_RejectsSpoofedContentType(t *testing.T) {
	t.Parallel()

	// A PDF dressed up as anything still sniffs as application/pdf.
	_, _, err := SniffImage(bytes.NewReader(pdfHeader))
	if !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("SniffImage(PDF bytes): err = %v, want ErrUnsupportedType", err)
	}
}

func TestSniffImage_RejectsExecutable(t *testing.T) {
	t.Parallel()

	// MZ header — Windows PE executables.
	_, _, err := SniffImage(bytes.NewReader([]byte("MZ\x90\x00\x03")))
	if !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("SniffImage(PE bytes): err = %v, want ErrUnsupportedType", err)
	}
}

// TestSniffImage_RejectsSVG documents the explicit decision to leave
// SVG out of the image allowlist — SVG can carry script and would be a
// stored-XSS vector if served back from the cover proxy.
func TestSniffImage_RejectsSVG(t *testing.T) {
	t.Parallel()

	_, _, err := SniffImage(strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	if !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("SniffImage(SVG): err = %v, want ErrUnsupportedType", err)
	}
}

// TestSniffImage_HeadReturnsConsumedBytes makes sure callers can
// reconstruct the original stream from the returned head + remaining
// reader. The cover handlers depend on this — they prepend `head` to
// the rest of the multipart body before persisting.
func TestSniffImage_HeadReturnsConsumedBytes(t *testing.T) {
	t.Parallel()

	// Pad past 512 to force a full sniff read.
	body := append([]byte{}, pngHeader...)
	body = append(body, bytes.Repeat([]byte{0x00}, 600)...)

	_, head, err := SniffImage(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("SniffImage: unexpected error %v", err)
	}
	if len(head) != 512 {
		t.Fatalf("head length = %d, want 512", len(head))
	}
	if !bytes.HasPrefix(head, pngHeader) {
		t.Error("head doesn't start with the PNG signature")
	}
}

func TestSniffEditionFile_AcceptsAllowedTypes(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"application/pdf": pdfHeader,
		// EPUB and CBZ files arrive as application/zip from
		// http.DetectContentType — the service layer pins format more
		// narrowly via the explicit `format` field.
		"application/zip": zipHeader,
		"audio/mpeg":      mp3Header,
	}
	for wantMime, head := range cases {
		t.Run(wantMime, func(t *testing.T) {
			got, _, err := SniffEditionFile(bytes.NewReader(head))
			if err != nil {
				t.Fatalf("SniffEditionFile(%s): unexpected error %v", wantMime, err)
			}
			if got != wantMime {
				t.Errorf("SniffEditionFile: got %q, want %q", got, wantMime)
			}
		})
	}
}

func TestSniffEditionFile_RejectsImage(t *testing.T) {
	t.Parallel()

	// An image being uploaded as an "edition file" is malformed input
	// — the cover endpoint exists for that.
	_, _, err := SniffEditionFile(bytes.NewReader(pngHeader))
	if !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("SniffEditionFile(PNG): err = %v, want ErrUnsupportedType", err)
	}
}
