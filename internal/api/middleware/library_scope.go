// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package middleware

import (
	"net/http"
	"strings"

	"github.com/fireball1725/librarium-api/internal/api/respond"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RequireLibraryPermission answers "may this person do X in library L". It
// doesn't answer "is the series in the path one of L's", and most handlers
// never asked: they loaded the record by its own id and ignored library_id. So
// an editor in one library could edit or delete another library's series,
// shelves, tags, loans and book metadata by pairing their own library's id with
// the other record's. RequireInLibrary closes that for every library-scoped
// route at once, by checking each id in the path against its parent.

// ownership is how one path id is tied to its parent: SQL answers whether the
// id ($1) belongs to the parent ($2), and Parent names the path value bound to $2.
type ownership struct {
	Parent string
	SQL    string
}

// Keyed by "segment/{param}" from the route pattern, so a param name reused
// for a different table elsewhere can't pick up the wrong check.
var owners = map[string]ownership{
	"series/{series_id}":              {"library_id", `SELECT EXISTS (SELECT 1 FROM series WHERE id = $1 AND library_id = $2)`},
	"arcs/{arc_id}":                   {"series_id", `SELECT EXISTS (SELECT 1 FROM series_arcs WHERE id = $1 AND series_id = $2)`},
	"shelves/{shelf_id}":              {"library_id", `SELECT EXISTS (SELECT 1 FROM shelves WHERE id = $1 AND library_id = $2)`},
	"tags/{tag_id}":                   {"library_id", `SELECT EXISTS (SELECT 1 FROM tags WHERE id = $1 AND library_id = $2)`},
	"loans/{loan_id}":                 {"library_id", `SELECT EXISTS (SELECT 1 FROM loans WHERE id = $1 AND library_id = $2)`},
	"proposals/{proposal_id}":         {"library_id", `SELECT EXISTS (SELECT 1 FROM ai_metadata_proposals WHERE id = $1 AND library_id = $2)`},
	"editions/{edition_id}":           {"book_id", `SELECT EXISTS (SELECT 1 FROM book_editions WHERE id = $1 AND book_id = $2)`},
	"files/{file_id}":                 {"edition_id", `SELECT EXISTS (SELECT 1 FROM edition_files WHERE id = $1 AND edition_id = $2)`},
	"storage-locations/{location_id}": {"library_id", `SELECT EXISTS (SELECT 1 FROM storage_locations WHERE id = $1 AND (library_id = $2 OR library_id IS NULL))`},
}

// A book belongs to every library holding a copy, and books are shared works,
// so reading one through any library stays open like the library-free
// /books/{book_id} route. Writing to it, or fetching one of its files, needs
// the library in the path to hold it.
const heldBook = `SELECT EXISTS (SELECT 1 FROM held_books WHERE book_id = $1 AND library_id = $2)`

// RequireInLibrary checks every id in the route against its parent, parents
// first, and answers 404 on the first that doesn't belong. It applies to
// instance admins too: a mismatched path is a wrong request, not a permission
// question. Must be chained after RequireLibraryPermission.
func RequireInLibrary(db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, c := range checksFor(r.Method, r.Pattern) {
				id, err1 := uuid.Parse(r.PathValue(c.param))
				parent, err2 := uuid.Parse(r.PathValue(c.Parent))
				if err1 != nil || err2 != nil {
					respond.Error(w, http.StatusBadRequest, "invalid "+c.param)
					return
				}
				var ok bool
				if err := db.QueryRow(r.Context(), c.SQL, id, parent).Scan(&ok); err != nil {
					respond.Error(w, http.StatusInternalServerError, "ownership check failed")
					return
				}
				if !ok {
					respond.Error(w, http.StatusNotFound, "not found in this library")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

type check struct {
	param string
	ownership
}

// checksFor lists the checks a route needs, in path order. pattern is the
// ServeMux pattern, such as "PUT /api/v1/libraries/{library_id}/series/{series_id}".
func checksFor(method, pattern string) []check {
	_, path, _ := strings.Cut(pattern, " ")
	rel, ok := strings.CutPrefix(path, "/api/v1/libraries/{library_id}")
	if !ok {
		return nil
	}
	segs := strings.Split(strings.Trim(rel, "/"), "/")
	var out []check
	for i := 1; i < len(segs); i++ {
		p := segs[i]
		if !strings.HasPrefix(p, "{") {
			continue
		}
		param := strings.Trim(p, "{}")
		if o, ok := owners[segs[i-1]+"/"+p]; ok {
			out = append(out, check{param, o})
			continue
		}
		// Only a book at the top of the path: under a series or a shelf the
		// parent check has already scoped it, and unlinking a book the library
		// no longer holds has to keep working.
		if i == 1 && p == "{book_id}" && needsHeldBook(method, rel) {
			out = append(out, check{param, ownership{"library_id", heldBook}})
		}
	}
	return out
}

func needsHeldBook(method, rel string) bool {
	if method == http.MethodPost && rel == "/books/{book_id}" {
		return false // adding a book the library doesn't hold yet
	}
	if method == http.MethodGet {
		return strings.Contains(rel, "/files/")
	}
	return true
}
