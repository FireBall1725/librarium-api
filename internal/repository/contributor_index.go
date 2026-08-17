// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/google/uuid"
)

// The Authors index: every contributor with books in the caller's libraries,
// with the counts and thumbnails the index cards need.
//
// A projection rather than models.Contributor, which is the domain entity and
// is already returned by six other paths. The index wants read counts, spine
// thumbnails and library membership that nothing else needs, and widening the
// shared struct for one screen makes every other caller pay to scan columns it
// throws away.

// AuthorSpine is one book thumbnail on an author card. Same shape as
// SeriesPreviewBook, and converted to a wire body the same way, so a thumbnail
// URL is built in one place rather than assembled by each client.
type AuthorSpine struct {
	BookID    uuid.UUID `json:"book_id"`
	Title     string    `json:"title"`
	HasCover  bool      `json:"has_cover"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AuthorLibraryRef names a library the author has books in, so the card can
// offer it as a jump into a filtered Books view.
type AuthorLibraryRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type AuthorIndexEntry struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	SortName string    `json:"sort_name"`
	HasPhoto bool      `json:"has_photo"`
	// Letter the index files this author under, taken from sort_name so
	// "Ursula K. Le Guin" appears under L rather than U. '#' for anything
	// that does not start with a letter.
	Letter    string             `json:"letter"`
	BookCount int                `json:"book_count"`
	ReadCount int                `json:"read_count"`
	Spines    []AuthorSpine      `json:"spines"`
	Libraries []AuthorLibraryRef `json:"libraries"`
}

// spinesPerCard is how many thumbnails a card shows. Five reads as a shelf
// without wrapping at the narrowest card width.
const spinesPerCard = 5

// defaultAuthorRoles is what the Authors index means by "author".
//
// book_contributors carries a role, so translators, illustrators and editors
// share the table. Listing them all under Authors would file a translator as
// the writer of a book they translated, so the index filters and the caller
// can widen it.
var defaultAuthorRoles = []string{"author"}

// ListAuthorIndex returns every contributor holding a matching role on a book
// in the given libraries.
//
// Unpaged on purpose. The index draws an A-Z bar that has to know which letters
// have anyone behind them, which means counting the whole set anyway; paging it
// would cost a second query to answer the same question. Author counts run in
// the hundreds, not the millions.
func (r *ContributorRepo) ListAuthorIndex(
	ctx context.Context,
	libraryIDs []uuid.UUID,
	callerID uuid.UUID,
	roles []string,
) ([]*AuthorIndexEntry, error) {
	if len(libraryIDs) == 0 {
		return []*AuthorIndexEntry{}, nil
	}
	if len(roles) == 0 {
		roles = defaultAuthorRoles
	}

	args := []any{libraryIDs, roles}

	// Read count is caller-relative, so it needs the caller. Without one every
	// author reads as nothing read, which is correct rather than an error: a
	// token with no user still gets a usable index.
	readCount := "0"
	if callerID != uuid.Nil {
		args = append(args, callerID)
		readCount = fmt.Sprintf(`COUNT(DISTINCT s.book_id) FILTER (WHERE EXISTS (
            SELECT 1 FROM user_book_interactions i
            JOIN book_editions e ON e.id = i.book_edition_id
            WHERE e.book_id = s.book_id AND i.user_id = $%d
              AND i.deleted_at IS NULL AND i.read_status = 'read'
        ))`, len(args))
	}

	// The scope CTE is DISTINCT on (contributor, book) so a book credited to
	// one person twice under two roles counts once. Without it an author with
	// both "author" and "editor" credits on the same book shows double.
	q := fmt.Sprintf(`
WITH scope AS (
    SELECT DISTINCT bc.contributor_id, b.id AS book_id
    FROM book_contributors bc
    JOIN books b ON b.id = bc.book_id
    JOIN library_books lb ON lb.book_id = b.id AND lb.deleted_at IS NULL
    WHERE lb.library_id = ANY($1) AND bc.role = ANY($2)
)
SELECT c.id,
       c.name,
       COALESCE(NULLIF(c.sort_name, ''), c.name) AS sort_name,
       EXISTS(SELECT 1 FROM cover_images ci
              WHERE ci.entity_type = 'contributor' AND ci.entity_id = c.id
                AND ci.is_primary = true) AS has_photo,
       COUNT(DISTINCT s.book_id)::int AS book_count,
       %s::int AS read_count,
       (
           SELECT COALESCE(json_agg(jsonb_build_object(
               'book_id', t.id, 'title', t.title,
               'has_cover', t.has_cover, 'updated_at', t.updated_at
           ) ORDER BY t.title), '[]'::json)
           FROM (
               SELECT DISTINCT b2.id, b2.title, b2.updated_at,
                      EXISTS(SELECT 1 FROM cover_images ci2
                             WHERE ci2.entity_type = 'book' AND ci2.entity_id = b2.id
                               AND ci2.is_primary = true) AS has_cover
               FROM book_contributors bc2
               JOIN books b2 ON b2.id = bc2.book_id
               JOIN library_books lb2 ON lb2.book_id = b2.id AND lb2.deleted_at IS NULL
               WHERE bc2.contributor_id = c.id AND bc2.role = ANY($2)
                 AND lb2.library_id = ANY($1)
               ORDER BY b2.title
               LIMIT %d
           ) t
       ) AS spines,
       (
           SELECT COALESCE(json_agg(DISTINCT jsonb_build_object('id', l.id, 'name', l.name)), '[]'::json)
           FROM library_books lb3
           JOIN libraries l ON l.id = lb3.library_id
           JOIN book_contributors bc3 ON bc3.book_id = lb3.book_id
           WHERE bc3.contributor_id = c.id AND bc3.role = ANY($2)
             AND lb3.library_id = ANY($1) AND lb3.deleted_at IS NULL
       ) AS libraries
FROM contributors c
JOIN scope s ON s.contributor_id = c.id
GROUP BY c.id, c.name, c.sort_name
ORDER BY lower(COALESCE(NULLIF(c.sort_name, ''), c.name)), c.name`, readCount, spinesPerCard)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing author index: %w", err)
	}
	defer rows.Close()

	out := make([]*AuthorIndexEntry, 0, 64)
	for rows.Next() {
		var (
			e         = &AuthorIndexEntry{}
			spineRaw  []byte
			libraries []byte
		)
		if err := rows.Scan(
			&e.ID, &e.Name, &e.SortName, &e.HasPhoto,
			&e.BookCount, &e.ReadCount, &spineRaw, &libraries,
		); err != nil {
			return nil, fmt.Errorf("scanning author index row: %w", err)
		}
		e.Spines = make([]AuthorSpine, 0, spinesPerCard)
		if len(spineRaw) > 0 {
			if err := json.Unmarshal(spineRaw, &e.Spines); err != nil {
				return nil, fmt.Errorf("decoding author spines: %w", err)
			}
		}
		e.Libraries = make([]AuthorLibraryRef, 0, 2)
		if len(libraries) > 0 {
			if err := json.Unmarshal(libraries, &e.Libraries); err != nil {
				return nil, fmt.Errorf("decoding author libraries: %w", err)
			}
		}
		e.Letter = indexLetter(e.SortName)
		out = append(out, e)
	}
	return out, rows.Err()
}

// indexLetter is the A-Z bucket a name files under. Anything that does not
// resolve to a letter goes to '#', which is where a reader looks for "1Q84" or
// a name in a script the bar does not cover.
//
// Decoded as a rune rather than a byte, and folded before bucketing: slicing
// one byte off a name starting with a multibyte character yields half a
// character, and filing Émile Zola under '#' rather than E is how an index
// loses the names it was built to find. NFD splits an accented letter into its
// base plus a combining mark, so the base is simply the first rune out.
func indexLetter(sortName string) string {
	trimmed := strings.TrimSpace(sortName)
	if trimmed == "" {
		return "#"
	}
	// Two "first rune of" steps, written as decodes rather than as range loops
	// that break on their first iteration: staticcheck reads those as loops
	// that cannot loop (SA4004), and it is right — nothing here repeats.
	first, _ := utf8.DecodeRuneInString(trimmed)
	base, _ := utf8.DecodeRuneInString(norm.NFD.String(string(first)))
	switch {
	case base >= 'a' && base <= 'z':
		return string(base - 32)
	case base >= 'A' && base <= 'Z':
		return string(base)
	}
	return "#"
}
