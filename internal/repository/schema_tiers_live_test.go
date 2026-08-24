// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/fireball1725/librarium-api/internal/models"
	"os"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These run the real queries against a restored collection, which is the only
// way to catch the things a compiler cannot: a composite foreign key that does
// not bite, a recursive view that does not terminate, a containment link that
// silently makes a work contain itself.
//
// Set LIBRARIUM_TEST_DSN to a database that has run migration 000025.

func tiersPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("LIBRARIUM_TEST_DSN")
	if dsn == "" {
		t.Skip("set LIBRARIUM_TEST_DSN to run")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)

	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.tables
		                WHERE table_schema = 'public' AND table_name = 'edition_identifiers')`).Scan(&exists); err != nil {
		t.Fatalf("checking for the tiers schema: %v", err)
	}
	if !exists {
		t.Skip("target database has not run migration 000025")
	}
	return pool, ctx
}

func TestIdentifiersResolveToTheirEdition(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewEditionIdentifierRepo(pool)

	// Whatever the collection actually holds, rather than a fixture.
	var wantEdition uuid.UUID
	var isbn string
	if err := pool.QueryRow(ctx, `
		SELECT edition_id, value FROM edition_identifiers WHERE scheme = 'isbn13' LIMIT 1`).
		Scan(&wantEdition, &isbn); err != nil {
		t.Skipf("no isbn13 identifiers: %v", err)
	}

	got, err := repo.FindEditionBy(ctx, "isbn13", isbn)
	if err != nil {
		t.Fatalf("finding edition by isbn13: %v", err)
	}
	if got != wantEdition {
		t.Errorf("resolved to %s, want %s", got, wantEdition)
	}

	// Step one of the matching chain has to be case-insensitive on the scheme,
	// since callers send "ISBN13" as readily as "isbn13".
	if got, err = repo.FindEditionBy(ctx, "ISBN13", isbn); err != nil || got != wantEdition {
		t.Errorf("uppercase scheme did not resolve: %s, %v", got, err)
	}

	if _, err := repo.FindEditionBy(ctx, "isbn13", "0000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a nonexistent identifier returned %v, want ErrNotFound", err)
	}
}

func TestAnIdentifierCannotBeClaimedTwice(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewEditionIdentifierRepo(pool)

	var takenBy uuid.UUID
	var isbn string
	if err := pool.QueryRow(ctx, `
		SELECT edition_id, value FROM edition_identifiers WHERE scheme = 'isbn13' LIMIT 1`).
		Scan(&takenBy, &isbn); err != nil {
		t.Skipf("no isbn13 identifiers: %v", err)
	}

	var other uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM book_editions WHERE id <> $1 LIMIT 1`, takenBy).Scan(&other); err != nil {
		t.Skipf("need a second edition: %v", err)
	}

	// This is the constraint the dedup logic always assumed and never had:
	// before the tiers migration, isbn_13 had a plain btree and two editions
	// could claim one ISBN.
	if err := repo.Add(ctx, other, "isbn13", isbn); !errors.Is(err, ErrIdentifierTaken) {
		t.Errorf("adding a taken ISBN to another edition returned %v, want ErrIdentifierTaken", err)
		_ = repo.Remove(ctx, other, "isbn13", isbn)
	}
}

func TestUnknownSchemeIsRejected(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewEditionIdentifierRepo(pool)

	var edition uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM book_editions LIMIT 1`).Scan(&edition); err != nil {
		t.Skipf("no editions: %v", err)
	}

	err := repo.Add(ctx, edition, "not_a_real_scheme", "whatever")
	if !errors.Is(err, ErrUnknownScheme) {
		t.Errorf("unknown scheme returned %v, want ErrUnknownScheme", err)
		_ = repo.Remove(ctx, edition, "not_a_real_scheme", "whatever")
	}
}

func TestContainmentRejectsCycles(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewBookContentsRepo(pool)

	var a, b uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM books ORDER BY created_at LIMIT 1`).Scan(&a); err != nil {
		t.Skipf("no books: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM books WHERE id <> $1 ORDER BY created_at LIMIT 1`, a).Scan(&b); err != nil {
		t.Skipf("need a second book: %v", err)
	}

	// Leave the catalogue as it was found; these links are not the fixture.
	cleanup := func() {
		_ = repo.Remove(ctx, a, b)
		_ = repo.Remove(ctx, b, a)
	}
	cleanup()
	defer cleanup()

	if err := repo.Add(ctx, a, a, 1); !errors.Is(err, ErrContainmentCycle) {
		t.Errorf("a work containing itself returned %v, want ErrContainmentCycle", err)
	}

	if err := repo.Add(ctx, a, b, 1); err != nil {
		t.Fatalf("adding a normal containment link: %v", err)
	}

	// The transitive case. Nothing in the database stops this on its own, and
	// letting it through means visible_books walks a loop forever.
	if err := repo.Add(ctx, b, a, 1); !errors.Is(err, ErrContainmentCycle) {
		t.Errorf("a two-step cycle returned %v, want ErrContainmentCycle", err)
	}

	contents, err := repo.ListContents(ctx, a)
	if err != nil {
		t.Fatalf("listing contents: %v", err)
	}
	if len(contents) != 1 || contents[0].ContainedID != b {
		t.Errorf("contents = %+v, want exactly the one link just added", contents)
	}

	containers, err := repo.ListContainers(ctx, b)
	if err != nil {
		t.Fatalf("listing containers: %v", err)
	}
	if len(containers) != 1 || containers[0].ContainerID != a {
		t.Errorf("containers = %+v, want the container just added", containers)
	}
}

func TestOwningAContainerReachesWhatItHolds(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewBookContentsRepo(pool)

	// A work someone holds a copy of, and one nobody does. The second is what
	// the container has to make visible.
	var held, unheld, userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT c.book_id, ur.user_id
		  FROM copies c
		  JOIN user_roles ur ON ur.library_id = c.library_id
		 WHERE c.deleted_at IS NULL
		 LIMIT 1`).Scan(&held, &userID); err != nil {
		t.Skipf("no held copies with a role: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT b.id FROM books b
		 WHERE NOT EXISTS (SELECT 1 FROM copies c WHERE c.book_id = b.id AND c.deleted_at IS NULL)
		   AND NOT EXISTS (SELECT 1 FROM user_books ub WHERE ub.book_id = b.id)
		   AND NOT EXISTS (SELECT 1 FROM wishlist w WHERE w.book_id = b.id)
		 LIMIT 1`).Scan(&unheld); err != nil {
		t.Skipf("no work that is invisible to begin with: %v", err)
	}

	visible := func(book uuid.UUID) bool {
		var ok bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM visible_books WHERE user_id = $1 AND book_id = $2)`,
			userID, book).Scan(&ok); err != nil {
			t.Fatalf("querying visible_books: %v", err)
		}
		return ok
	}

	if visible(unheld) {
		t.Skip("the chosen work is already visible some other way")
	}

	defer func() { _ = repo.Remove(ctx, held, unheld) }()
	if err := repo.Add(ctx, held, unheld, 1); err != nil {
		t.Fatalf("adding containment: %v", err)
	}

	// This is the whole point of book_contents: buy the omnibus, and the
	// volumes inside it stop reading as missing.
	if !visible(unheld) {
		t.Error("a work inside a held container is not visible; ownership does not resolve through containment")
	}

	_ = repo.Remove(ctx, held, unheld)
	if visible(unheld) {
		t.Error("the work stayed visible after the containment link was removed")
	}
}

func TestReadStatusInheritsThroughContainmentButExplicitWins(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewBookContentsRepo(pool)

	var userID, container, contained uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT ub.user_id, ub.book_id FROM user_books ub
		 WHERE ub.read_status = 'read' AND ub.deleted_at IS NULL LIMIT 1`).Scan(&userID, &container); err != nil {
		t.Skipf("no work marked read: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT b.id FROM books b
		 WHERE b.id <> $1
		   AND NOT EXISTS (SELECT 1 FROM user_books ub WHERE ub.book_id = b.id AND ub.user_id = $2)
		 LIMIT 1`, container, userID).Scan(&contained); err != nil {
		t.Skipf("no work without an opinion from this user: %v", err)
	}

	defer func() { _ = repo.Remove(ctx, container, contained) }()
	if err := repo.Add(ctx, container, contained, 1); err != nil {
		t.Fatalf("adding containment: %v", err)
	}

	var status string
	var inherited bool
	err := pool.QueryRow(ctx, `
		SELECT read_status, inherited FROM effective_read_status
		 WHERE user_id = $1 AND book_id = $2`, userID, contained).Scan(&status, &inherited)
	if err != nil {
		t.Fatalf("querying effective_read_status: %v", err)
	}
	if status != "read" || !inherited {
		t.Errorf("status = %q inherited = %v, want read and inherited; visibility recursed through containment "+
			"from the start and reading state has to as well", status, inherited)
	}
}

func TestACopyCannotClaimAnotherWorksEdition(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewCopyRepo(pool)

	var libraryID, bookID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT library_id, book_id FROM copies WHERE deleted_at IS NULL LIMIT 1`).
		Scan(&libraryID, &bookID); err != nil {
		t.Skipf("no copies: %v", err)
	}

	var foreignEdition uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM book_editions WHERE book_id <> $1 LIMIT 1`, bookID).Scan(&foreignEdition); err != nil {
		t.Skipf("need an edition of another work: %v", err)
	}

	// The bug an outside reviewer found: a plain foreign key on edition_id lets
	// a copy claim Dune the work with a Neuromancer edition, silently.
	got, err := repo.Create(ctx, CreateCopyInput{
		LibraryID: libraryID,
		BookID:    bookID,
		EditionID: &foreignEdition,
	})
	if !errors.Is(err, ErrEditionNotOfBook) {
		t.Errorf("creating a mismatched copy returned %v, want ErrEditionNotOfBook", err)
		if got != nil {
			_ = repo.Delete(ctx, got.ID)
		}
	}
}

func TestACopyCannotUseAnotherLibrarysLocation(t *testing.T) {
	pool, ctx := tiersPool(t)
	copies := NewCopyRepo(pool)
	locations := NewCopyLocationRepo(pool)

	var libA, libB uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries ORDER BY created_at LIMIT 1`).Scan(&libA); err != nil {
		t.Skipf("no libraries: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM libraries WHERE id <> $1 LIMIT 1`, libA).Scan(&libB); err != nil {
		t.Skipf("need a second library: %v", err)
	}
	var bookID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT book_id FROM copies WHERE library_id = $1 AND deleted_at IS NULL LIMIT 1`, libA).Scan(&bookID); err != nil {
		t.Skipf("no copies in the first library: %v", err)
	}

	elsewhere, err := locations.Create(ctx, libB, "test shelf in the other library", nil)
	if err != nil {
		t.Fatalf("creating a location: %v", err)
	}
	defer func() { _ = locations.Delete(ctx, elsewhere.ID) }()

	got, err := copies.Create(ctx, CreateCopyInput{
		LibraryID:  libA,
		BookID:     bookID,
		LocationID: &elsewhere.ID,
	})
	if !errors.Is(err, ErrLocationNotInLibrary) {
		t.Errorf("filing a copy at another library's shelf returned %v, want ErrLocationNotInLibrary", err)
		if got != nil {
			_ = copies.Delete(ctx, got.ID)
		}
	}
}

func TestLocationTreeRefusesLoopsAndKeepsFullShelves(t *testing.T) {
	pool, ctx := tiersPool(t)
	locations := NewCopyLocationRepo(pool)
	copies := NewCopyRepo(pool)

	var libraryID, bookID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT library_id, book_id FROM copies WHERE deleted_at IS NULL LIMIT 1`).
		Scan(&libraryID, &bookID); err != nil {
		t.Skipf("no copies: %v", err)
	}

	office, err := locations.Create(ctx, libraryID, "test office", nil)
	if err != nil {
		t.Fatalf("creating parent location: %v", err)
	}
	defer func() { _ = locations.Delete(ctx, office.ID) }()

	shelf, err := locations.Create(ctx, libraryID, "test top shelf", &office.ID)
	if err != nil {
		t.Fatalf("creating child location: %v", err)
	}
	defer func() { _ = locations.Delete(ctx, shelf.ID) }()

	// Moving the parent under its own child is the loop that hangs every tree
	// walk, and nothing in the schema stops it on its own.
	if _, err := locations.Rename(ctx, office.ID, "test office", &shelf.ID, true); !errors.Is(err, ErrLocationCycle) {
		t.Errorf("reparenting under a descendant returned %v, want ErrLocationCycle", err)
	}

	// A shelf with something on it must not vanish: a copy whose location
	// silently became null is a book you cannot find.
	copy, err := copies.Create(ctx, CreateCopyInput{
		LibraryID: libraryID, BookID: bookID, LocationID: &shelf.ID,
	})
	if err != nil {
		t.Fatalf("creating a copy at the shelf: %v", err)
	}
	defer func() { _ = copies.Delete(ctx, copy.ID) }()

	if err := locations.Delete(ctx, shelf.ID); !errors.Is(err, ErrLocationInUse) {
		t.Errorf("deleting an occupied shelf returned %v, want ErrLocationInUse", err)
	}

	if copy.LocationName != "test top shelf" {
		t.Errorf("copy location name = %q, want the shelf it was filed at", copy.LocationName)
	}
}

func TestPartialUpsertDoesNotClobberOtherFields(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewUserBookRepo(pool)

	var userID, bookID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT u.id, b.id FROM users u CROSS JOIN books b
		 WHERE NOT EXISTS (SELECT 1 FROM user_books ub WHERE ub.user_id = u.id AND ub.book_id = b.id)
		 LIMIT 1`).Scan(&userID, &bookID); err != nil {
		t.Skipf("no untouched user/book pair: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM user_books WHERE user_id = $1 AND book_id = $2`, userID, bookID)
	}()

	status := "read"
	rating := 8
	review := "worth the reread"
	if _, err := repo.Upsert(ctx, userID, bookID, UpsertInput{
		ReadStatus: &status, Rating: &[]*int{&rating}[0], Review: &review,
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// A client that only knows about favourites must not blank the review the
	// user typed on another device. This is the whole reason every field is a
	// pointer.
	fav := true
	got, err := repo.Upsert(ctx, userID, bookID, UpsertInput{IsFavorite: &fav})
	if err != nil {
		t.Fatalf("partial upsert: %v", err)
	}
	if got.Review != review {
		t.Errorf("review = %q after a favourite-only update, want it untouched", got.Review)
	}
	if got.Rating == nil || *got.Rating != rating {
		t.Errorf("rating = %v after a favourite-only update, want %d", got.Rating, rating)
	}
	if got.ReadStatus != status {
		t.Errorf("read status = %q, want %q", got.ReadStatus, status)
	}
	if !got.IsFavorite {
		t.Error("is_favorite did not take")
	}

	// Clearing is different from not mentioning, and one parameter cannot say
	// both, which is why Rating is a pointer to a pointer.
	var cleared *int
	got, err = repo.Upsert(ctx, userID, bookID, UpsertInput{Rating: &cleared})
	if err != nil {
		t.Fatalf("clearing the rating: %v", err)
	}
	if got.Rating != nil {
		t.Errorf("rating = %v after an explicit clear, want nil", got.Rating)
	}
	if got.Review != review {
		t.Errorf("clearing the rating also changed the review to %q", got.Review)
	}
}

func TestSmartListsRefuseHandPickedBooks(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewListRepo(pool)

	var userID, bookID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no users: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM books LIMIT 1`).Scan(&bookID); err != nil {
		t.Skipf("no books: %v", err)
	}

	smart, err := repo.Create(ctx, CreateListInput{
		OwnerUserID: userID, Name: "test smart list", Kind: "smart",
		Filter: []byte(`{"read_status":"unread"}`),
	})
	if err != nil {
		t.Fatalf("creating a smart list: %v", err)
	}
	defer func() { _ = repo.Delete(ctx, smart.ID) }()

	if smart.FilterVersion == nil || *smart.FilterVersion != FilterVersionCurrent {
		t.Errorf("filter version = %v, want %d; an unversioned filter gets silently reinterpreted later",
			smart.FilterVersion, FilterVersionCurrent)
	}

	// A smart list computes its own membership, so adding by hand would produce
	// a row its filter disagrees with.
	if err := repo.AddBook(ctx, smart.ID, bookID, 0); !errors.Is(err, ErrSmartListNotEnumerable) {
		t.Errorf("adding a book to a smart list returned %v, want ErrSmartListNotEnumerable", err)
	}

	manual, err := repo.Create(ctx, CreateListInput{
		OwnerUserID: userID, Name: "test manual list", Kind: "manual",
	})
	if err != nil {
		t.Fatalf("creating a manual list: %v", err)
	}
	defer func() { _ = repo.Delete(ctx, manual.ID) }()

	if err := repo.AddBook(ctx, manual.ID, bookID, 1); err != nil {
		t.Fatalf("adding a book to a manual list: %v", err)
	}
	ids, err := repo.BookIDs(ctx, manual.ID)
	if err != nil {
		t.Fatalf("reading list contents: %v", err)
	}
	if len(ids) != 1 || ids[0] != bookID {
		t.Errorf("contents = %v, want the one book added", ids)
	}
}

func TestPublicListsGetAnUnguessableToken(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewListRepo(pool)

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no users: %v", err)
	}

	public, err := repo.Create(ctx, CreateListInput{
		OwnerUserID: userID, Name: "test public list", Kind: "manual", Visibility: "public",
	})
	if err != nil {
		t.Fatalf("creating a public list: %v", err)
	}
	defer func() { _ = repo.Delete(ctx, public.ID) }()

	// The token is the only credential on a public link, so guessing it has to
	// be pointless.
	if len(public.ShareToken) < 40 {
		t.Errorf("share token %q is too short to be unguessable", public.ShareToken)
	}

	found, err := repo.FindByShareToken(ctx, public.ShareToken)
	if err != nil || found.ID != public.ID {
		t.Errorf("resolving the share token gave %v, %v", found, err)
	}

	if _, err := repo.FindByShareToken(ctx, "not-a-real-token"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a bogus token returned %v, want ErrNotFound", err)
	}

	// A private list must not be reachable by a token left over from before.
	private, err := repo.Create(ctx, CreateListInput{
		OwnerUserID: userID, Name: "test private list", Kind: "manual",
	})
	if err != nil {
		t.Fatalf("creating a private list: %v", err)
	}
	defer func() { _ = repo.Delete(ctx, private.ID) }()
	if private.ShareToken != "" {
		t.Errorf("a private list was given a share token: %q", private.ShareToken)
	}
}

func TestWishlistHoldsBothShapesInOneTable(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewWishlistRepo(pool)

	var userID, bookID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no users: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT b.id FROM books b
		 WHERE NOT EXISTS (SELECT 1 FROM wishlist w WHERE w.book_id = b.id AND w.user_id = $1)
		 LIMIT 1`, userID).Scan(&bookID); err != nil {
		t.Skipf("no book that is not already wanted: %v", err)
	}

	catalogued, err := repo.AddCatalogued(ctx, userID, bookID, "seen in a shop", 5)
	if err != nil {
		t.Fatalf("adding a catalogued want: %v", err)
	}
	defer func() { _ = repo.Remove(ctx, userID, catalogued.ID) }()

	if catalogued.Title == "" {
		t.Error("a catalogued want has no title; it should read through to the book")
	}
	if err := func() error { _, err := repo.AddCatalogued(ctx, userID, bookID, "", 0); return err }(); !errors.Is(err, ErrAlreadyWanted) {
		t.Errorf("wanting the same book twice returned %v, want ErrAlreadyWanted", err)
	}

	free, err := repo.AddFreeText(ctx, userID, "Something With No ISBN", "A Nobody", "", 1)
	if err != nil {
		t.Fatalf("adding a free-text want: %v", err)
	}
	defer func() { _ = repo.Remove(ctx, userID, free.ID) }()
	if free.BookID != nil {
		t.Error("a free-text want should not point at a book")
	}

	// Both shapes come back from one query, which is the point: the old schema
	// split this by whether the thing was catalogued.
	all, err := repo.List(ctx, userID)
	if err != nil {
		t.Fatalf("listing the wishlist: %v", err)
	}
	var sawBoth int
	for _, e := range all {
		if e.ID == catalogued.ID || e.ID == free.ID {
			sawBoth++
		}
	}
	if sawBoth != 2 {
		t.Errorf("one list returned %d of the 2 entries just added", sawBoth)
	}
}

// TestPermissionCheckIsUnchangedByTheRoleMigration compares the query the
// middleware used to run against the one it runs now, for every combination of
// real user, real library and real permission.
//
// This is the test that matters most in the whole tier change. The permission
// path guards 88 routes, and a difference here is not a wrong count on a page,
// it is someone seeing or changing what they should not.
func TestPermissionCheckIsUnchangedByTheRoleMigration(t *testing.T) {
	pool, ctx := tiersPool(t)

	var users, libraries []uuid.UUID
	var permissions []string

	rows, err := pool.Query(ctx, `SELECT id FROM users`)
	if err != nil {
		t.Fatalf("listing users: %v", err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		users = append(users, id)
	}
	rows.Close()

	rows, err = pool.Query(ctx, `SELECT id FROM libraries`)
	if err != nil {
		t.Fatalf("listing libraries: %v", err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		libraries = append(libraries, id)
	}
	rows.Close()

	rows, err = pool.Query(ctx, `SELECT name FROM permissions`)
	if err != nil {
		t.Fatalf("listing permissions: %v", err)
	}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		permissions = append(permissions, p)
	}
	rows.Close()

	if len(users) == 0 || len(libraries) == 0 || len(permissions) == 0 {
		t.Skip("need users, libraries and permissions to compare")
	}

	// What the middleware ran before: library_memberships joined to the role's
	// permissions, with instance admins bypassed in Go rather than in SQL.
	const oldQ = `
		SELECT COUNT(*) > 0
		  FROM library_memberships lm
		  JOIN role_permissions rp ON rp.role_id = lm.role_id
		  JOIN permissions p       ON p.id = rp.permission_id
		 WHERE lm.library_id = $1 AND lm.user_id = $2 AND p.name = $3`

	// What it runs now: user_roles, where a null library_id is an instance-wide
	// grant and so covers the case the Go bypass used to handle.
	const newQ = `
		SELECT EXISTS (
		    SELECT 1
		      FROM user_roles ur
		      JOIN role_permissions rp ON rp.role_id = ur.role_id
		      JOIN permissions p       ON p.id = rp.permission_id
		     WHERE ur.user_id = $2 AND p.name = $3
		       AND (ur.library_id IS NULL OR ur.library_id = $1))`

	var compared, differed int
	for _, u := range users {
		var isAdmin bool
		if err := pool.QueryRow(ctx, `SELECT is_instance_admin FROM users WHERE id = $1`, u).Scan(&isAdmin); err != nil {
			t.Fatalf("reading admin flag: %v", err)
		}
		for _, l := range libraries {
			for _, p := range permissions {
				var before, after bool
				if err := pool.QueryRow(ctx, oldQ, l, u, p).Scan(&before); err != nil {
					t.Fatalf("old query: %v", err)
				}
				if err := pool.QueryRow(ctx, newQ, l, u, p).Scan(&after); err != nil {
					t.Fatalf("new query: %v", err)
				}
				// The old path let instance admins through before reaching SQL,
				// so the effective answer then was "membership grants it OR the
				// user is an admin".
				effectiveBefore := before || isAdmin
				compared++
				if effectiveBefore != after {
					differed++
					if differed <= 5 {
						t.Errorf("permission %q for user %s in library %s: was %v, now %v",
							p, u, l, effectiveBefore, after)
					}
				}
			}
		}
	}

	t.Logf("compared %d user/library/permission combinations", compared)
	if differed > 0 {
		t.Errorf("%d of %d combinations changed answer", differed, compared)
	}
}

func TestRoleGrantsRespectScope(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewUserRoleRepo(pool)

	var userID, libraryID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no users: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no libraries: %v", err)
	}

	var libraryRole, instanceRole uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM roles WHERE scope = 'library' LIMIT 1`).Scan(&libraryRole); err != nil {
		t.Skipf("no library-scoped role: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM roles WHERE scope = 'instance' LIMIT 1`).Scan(&instanceRole); err != nil {
		t.Skipf("no instance-scoped role: %v", err)
	}

	// Granting library_viewer across the whole instance is meaningless, and
	// granting instance_admin on one library would scope admin:users to a
	// library, which is not a thing.
	if err := repo.Grant(ctx, userID, libraryRole, nil, nil); !errors.Is(err, ErrRoleScopeMismatch) {
		t.Errorf("granting a library role instance-wide returned %v, want ErrRoleScopeMismatch", err)
		_ = repo.Revoke(ctx, userID, libraryRole, nil)
	}
	if err := repo.Grant(ctx, userID, instanceRole, &libraryID, nil); !errors.Is(err, ErrRoleScopeMismatch) {
		t.Errorf("pinning an instance role to a library returned %v, want ErrRoleScopeMismatch", err)
		_ = repo.Revoke(ctx, userID, instanceRole, &libraryID)
	}
}

func TestReadableLibrariesMatchesWhatTheOldMembershipQuerySaid(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewUserRoleRepo(pool)

	rows, err := pool.Query(ctx, `SELECT id, is_instance_admin FROM users`)
	if err != nil {
		t.Fatalf("listing users: %v", err)
	}
	type u struct {
		id    uuid.UUID
		admin bool
	}
	var all []u
	for rows.Next() {
		var x u
		if err := rows.Scan(&x.id, &x.admin); err != nil {
			t.Fatal(err)
		}
		all = append(all, x)
	}
	rows.Close()

	for _, user := range all {
		got, err := repo.ReadableLibraryIDs(ctx, user.id)
		if err != nil {
			t.Fatalf("resolving readable libraries: %v", err)
		}

		var want int
		q := `SELECT count(*) FROM library_memberships lm
		       JOIN libraries l ON l.id = lm.library_id
		      WHERE lm.user_id = $1 AND lm.deleted_at IS NULL`
		if user.admin {
			q = `SELECT count(*) FROM libraries`
		}
		args := []any{user.id}
		if user.admin {
			args = nil
		}
		if err := pool.QueryRow(ctx, q, args...).Scan(&want); err != nil {
			t.Fatalf("counting old membership: %v", err)
		}

		if len(got) != want {
			t.Errorf("user %s can read %d libraries, old membership said %d", user.id, len(got), want)
		}
	}
}

// TestHeldBooksMatchesTheJunctionItReplaced pins the switch from library_books
// to copies.
//
// Every list query that used to join the junction now joins held_books, which
// collapses copies back to one row per work. If those two disagree, books
// appear or vanish from people's libraries, so the equivalence is asserted
// rather than assumed. The old table is still present and still holds what it
// held before the tiers migration, which is what makes this comparison possible
// at all; it stops being possible at contract.
func TestHeldBooksMatchesTheJunctionItReplaced(t *testing.T) {
	pool, ctx := tiersPool(t)

	var junction, held int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM library_books WHERE deleted_at IS NULL`).Scan(&junction); err != nil {
		t.Skipf("no library_books table: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM held_books`).Scan(&held); err != nil {
		t.Fatalf("querying held_books: %v", err)
	}
	if junction != held {
		t.Errorf("held_books has %d rows, library_books has %d", held, junction)
	}

	// Counts agreeing is weaker than the sets agreeing, and the sets are what
	// a list renders.
	var onlyOld, onlyNew int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
		    SELECT library_id, book_id FROM library_books WHERE deleted_at IS NULL
		    EXCEPT SELECT library_id, book_id FROM held_books) t`).Scan(&onlyOld); err != nil {
		t.Fatalf("comparing sets: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
		    SELECT library_id, book_id FROM held_books
		    EXCEPT SELECT library_id, book_id FROM library_books WHERE deleted_at IS NULL) t`).Scan(&onlyNew); err != nil {
		t.Fatalf("comparing sets: %v", err)
	}
	if onlyOld != 0 {
		t.Errorf("%d holdings exist in library_books but not in held_books; those books vanished from their library", onlyOld)
	}
	if onlyNew != 0 {
		t.Errorf("%d holdings exist in held_books but not in library_books; those books appeared from nowhere", onlyNew)
	}
}

// TestHeldBooksCollapsesDuplicateCopies is the failure mode a naive swap of
// library_books for copies would have shipped: copies is one row per object, so
// owning two of something would list it twice.
func TestHeldBooksCollapsesDuplicateCopies(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewCopyRepo(pool)

	var libraryID, bookID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT library_id, book_id FROM copies WHERE deleted_at IS NULL LIMIT 1`).
		Scan(&libraryID, &bookID); err != nil {
		t.Skipf("no copies: %v", err)
	}

	count := func() int {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM held_books WHERE library_id = $1 AND book_id = $2`,
			libraryID, bookID).Scan(&n); err != nil {
			t.Fatalf("counting held_books: %v", err)
		}
		return n
	}

	if before := count(); before != 1 {
		t.Fatalf("held_books already reports %d rows for one holding, want 1", before)
	}

	second, err := repo.Create(ctx, CreateCopyInput{LibraryID: libraryID, BookID: bookID})
	if err != nil {
		t.Fatalf("creating a second copy: %v", err)
	}
	defer func() { _ = repo.Delete(ctx, second.ID) }()

	if after := count(); after != 1 {
		t.Errorf("held_books reports %d rows after a second copy was added, want 1; "+
			"every list joining it would show the book twice", after)
	}

	// And retiring the extra must not remove the holding entirely.
	if err := repo.Delete(ctx, second.ID); err != nil {
		t.Fatalf("deleting the second copy: %v", err)
	}
	if after := count(); after != 1 {
		t.Errorf("held_books reports %d rows after the extra copy was retired, want 1", after)
	}
}

// TestBuiltInListsSeedOnceAndResistDeletion covers what the rail depends on: a
// user who has never asked for lists gets the shipped ones, asking twice does
// not double them, and the one the books page opens on cannot be deleted.
func TestBuiltInListsSeedOnceAndResistDeletion(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewListRepo(pool)

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no users: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx,
			`DELETE FROM lists WHERE owner_user_id = $1 AND builtin_key IS NOT NULL`, userID)
	}()
	// Start from a known state rather than assuming this user has none: the
	// seed is idempotent, so a previous run leaving rows behind must not change
	// what this measures.
	if _, err := pool.Exec(ctx,
		`DELETE FROM lists WHERE owner_user_id = $1 AND builtin_key IS NOT NULL`, userID); err != nil {
		t.Fatalf("clearing built-ins: %v", err)
	}

	if err := repo.SeedBuiltIns(ctx, userID); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	// Twice, because it runs on every read.
	if err := repo.SeedBuiltIns(ctx, userID); err != nil {
		t.Fatalf("seeding a second time: %v", err)
	}

	var seeded int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM lists WHERE owner_user_id = $1 AND builtin_key IS NOT NULL`,
		userID).Scan(&seeded); err != nil {
		t.Fatalf("counting built-ins: %v", err)
	}
	if seeded != len(BuiltinLists) {
		t.Errorf("seeding twice left %d built-ins, want %d", seeded, len(BuiltinLists))
	}

	var defaultID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM lists WHERE owner_user_id = $1 AND builtin_key = 'default'`,
		userID).Scan(&defaultID); err != nil {
		t.Fatalf("finding the default list: %v", err)
	}
	list, err := repo.FindByID(ctx, defaultID)
	if err != nil {
		t.Fatalf("reading the default list: %v", err)
	}
	// Hidden and Permanent are looked up from the key, not stored, so a read
	// that does not resolve them would return a deletable Default.
	if !list.Hidden || !list.Permanent {
		t.Errorf("default list: hidden=%v permanent=%v, want both true", list.Hidden, list.Permanent)
	}
	if list.Kind != "smart" || len(list.Filter) == 0 {
		t.Errorf("default list kind=%q filter=%q, want a smart list with a filter", list.Kind, list.Filter)
	}
	if err := repo.Delete(ctx, defaultID); !errors.Is(err, ErrListPermanent) {
		t.Errorf("deleting the default returned %v, want ErrListPermanent", err)
	}

	// A built-in that is not permanent goes, and a rename does not make it
	// claim to be something else.
	var readingID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM lists WHERE owner_user_id = $1 AND builtin_key = 'reading'`,
		userID).Scan(&readingID); err != nil {
		t.Fatalf("finding the reading list: %v", err)
	}
	renamed := "Currently reading"
	updated, err := repo.Update(ctx, readingID, UpdateListInput{Name: &renamed})
	if err != nil {
		t.Fatalf("renaming a built-in: %v", err)
	}
	if updated.Name != renamed || updated.BuiltinKey != "reading" {
		t.Errorf("after rename: name=%q key=%q, want %q and reading", updated.Name, updated.BuiltinKey, renamed)
	}
	if err := repo.Delete(ctx, readingID); err != nil {
		t.Errorf("deleting a non-permanent built-in: %v", err)
	}
}

// TestListFilterReachesTheWireAsJSON guards the shape clients are about to
// adopt. Filter was []byte, which marshals to base64, so the filter arrived as
// an opaque string a client had to decode before it could read the query it had
// itself written.
func TestListFilterReachesTheWireAsJSON(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewListRepo(pool)

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no users: %v", err)
	}
	l, err := repo.Create(ctx, CreateListInput{
		OwnerUserID: userID, Name: "test wire shape", Kind: "smart",
		Filter: []byte(`{"read_status":"unread"}`),
	})
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	defer func() { _ = repo.Delete(ctx, l.ID) }()

	encoded, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}
	if _, ok := wire["id"]; !ok {
		t.Errorf("no id key on the wire; keys are %v", keysOf(wire))
	}
	filter, ok := wire["filter"].(map[string]any)
	if !ok {
		t.Fatalf("filter came back as %T, want an object", wire["filter"])
	}
	if filter["read_status"] != "unread" {
		t.Errorf("filter = %v, want the stored query", filter)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestShelfWritesLandInLists covers the switch: shelf routes still exist and
// still work, but the rows they write are lists, so a shelf made through the
// old route is visible to the new one.
func TestShelfWritesLandInLists(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewShelfRepo(pool)

	var userID, libraryID, bookID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no users: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no libraries: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM books LIMIT 1`).Scan(&bookID); err != nil {
		t.Skipf("no books: %v", err)
	}

	shelfID := uuid.New()
	shelf, err := repo.Create(ctx, shelfID, libraryID, "test shelf", "desc", "#fff", "star", 3, userID)
	if err != nil {
		t.Fatalf("creating a shelf: %v", err)
	}
	defer func() { _ = repo.Delete(ctx, shelfID) }()
	if shelf.Name != "test shelf" {
		t.Errorf("shelf name = %q, want the one written", shelf.Name)
	}

	var kind, visibility string
	var sharedLibrary uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT kind, visibility, shared_library_id FROM lists WHERE id = $1`,
		shelfID).Scan(&kind, &visibility, &sharedLibrary); err != nil {
		t.Fatalf("the shelf did not land in lists: %v", err)
	}
	if kind != "manual" || visibility != "library" || sharedLibrary != libraryID {
		t.Errorf("stored as kind=%q visibility=%q library=%v, want a manual list shared with %v",
			kind, visibility, sharedLibrary, libraryID)
	}

	if _, err := repo.Update(ctx, shelfID, "renamed shelf", "", "", "", 5); err != nil {
		t.Fatalf("updating a shelf: %v", err)
	}
	if err := repo.AddBook(ctx, shelfID, bookID, userID); err != nil {
		t.Fatalf("adding a book to a shelf: %v", err)
	}
	var members int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM list_books WHERE list_id = $1 AND book_id = $2`,
		shelfID, bookID).Scan(&members); err != nil {
		t.Fatalf("counting membership: %v", err)
	}
	if members != 1 {
		t.Errorf("list_books holds %d rows for the book, want 1", members)
	}
	if err := repo.RemoveBook(ctx, shelfID, bookID); err != nil {
		t.Fatalf("removing a book from a shelf: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM list_books WHERE list_id = $1 AND book_id = $2`,
		shelfID, bookID).Scan(&members); err != nil {
		t.Fatalf("counting membership after removal: %v", err)
	}
	if members != 0 {
		t.Errorf("list_books still holds %d rows after removal", members)
	}
}

// TestVocabulariesAreReadableAndNonEmpty covers the point of making these
// tables at all. A format list a client cannot read is a format list the client
// hardcodes, which puts the vocabulary back in a release.
func TestVocabulariesAreReadableAndNonEmpty(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewVocabularyRepo(pool)

	cases := []struct {
		name string
		read func(context.Context) ([]*models.Vocabulary, error)
		// appliesTo is set for the one vocabulary that carries it.
		appliesTo bool
	}{
		{"edition formats", repo.EditionFormats, false},
		{"copy conditions", repo.CopyConditions, false},
		{"contributor roles", repo.ContributorRoles, true},
	}
	for _, c := range cases {
		items, err := c.read(ctx)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if len(items) == 0 {
			t.Errorf("%s came back empty; the seed in 000025 should have filled it", c.name)
			continue
		}
		for _, v := range items {
			if v.Code == "" {
				t.Errorf("%s: a row has no code", c.name)
			}
			if !v.IsActive {
				t.Errorf("%s: %q is inactive but was offered anyway", c.name, v.Code)
			}
			if c.appliesTo && v.AppliesTo == "" {
				t.Errorf("%s: %q has no applies_to, so a caller cannot tell whether it describes the work or the printing", c.name, v.Code)
			}
			if !c.appliesTo && v.AppliesTo != "" {
				t.Errorf("%s: %q carries applies_to=%q, which only contributor roles have", c.name, v.Code, v.AppliesTo)
			}
		}
	}
}

// TestLibraryMembersMatchesTheTableItReplaced pins the switch from
// library_memberships to a view over user_roles. The two must name the same
// people, or somebody either lost access or gained it.
func TestLibraryMembersMatchesTheTableItReplaced(t *testing.T) {
	pool, ctx := tiersPool(t)

	type pair struct{ library, user uuid.UUID }
	read := func(q string) map[pair]bool {
		rows, err := pool.Query(ctx, q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		defer rows.Close()
		out := map[pair]bool{}
		for rows.Next() {
			var p pair
			if err := rows.Scan(&p.library, &p.user); err != nil {
				t.Fatalf("scanning: %v", err)
			}
			out[p] = true
		}
		return out
	}

	was := read(`SELECT library_id, user_id FROM library_memberships WHERE deleted_at IS NULL`)
	now := read(`SELECT library_id, user_id FROM library_members`)
	if len(was) == 0 {
		t.Skip("no memberships to compare")
	}
	for p := range was {
		if !now[p] {
			t.Errorf("user %s lost access to library %s in the switch", p.user, p.library)
		}
	}
	for p := range now {
		if !was[p] {
			t.Errorf("user %s gained access to library %s in the switch", p.user, p.library)
		}
	}
}

// TestLibraryMembersCollapsesASecondRole covers the cardinality trap. user_roles
// has no unique constraint on (library_id, user_id) because a grant table has to
// be able to say someone holds two roles; every reader of the old table assumed
// exactly one. A second grant must not make a person appear twice, or the books
// list joined through it shows every book twice.
func TestLibraryMembersCollapsesASecondRole(t *testing.T) {
	pool, ctx := tiersPool(t)

	var libraryID, userID, roleID uuid.UUID
	err := pool.QueryRow(ctx,
		`SELECT library_id, user_id, role_id FROM library_members LIMIT 1`).
		Scan(&libraryID, &userID, &roleID)
	if err != nil {
		t.Skipf("no members: %v", err)
	}

	// A different library-scoped role than the one already held.
	var otherRole uuid.UUID
	err = pool.QueryRow(ctx,
		`SELECT id FROM roles WHERE scope = 'library' AND id <> $1 LIMIT 1`, roleID).Scan(&otherRole)
	if err != nil {
		t.Skipf("no second library role to grant: %v", err)
	}

	grantID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_roles (id, user_id, role_id, scope, library_id)
		 VALUES ($1, $2, $3, 'library', $4)`, grantID, userID, otherRole, libraryID); err != nil {
		t.Fatalf("granting a second role: %v", err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM user_roles WHERE id = $1`, grantID) }()

	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM library_members WHERE library_id = $1 AND user_id = $2`,
		libraryID, userID).Scan(&rows); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if rows != 1 {
		t.Errorf("two grants produced %d member rows, want 1", rows)
	}

	// The surviving row reports the more privileged of the two, measured by how
	// many permissions the role actually grants.
	var reported uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT role_id FROM library_members WHERE library_id = $1 AND user_id = $2`,
		libraryID, userID).Scan(&reported); err != nil {
		t.Fatalf("reading the collapsed role: %v", err)
	}
	var reportedPerms, heldPerms int
	if err := pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM role_permissions WHERE role_id = $1),
		        (SELECT count(*) FROM role_permissions WHERE role_id = $2)`,
		reported, roleID).Scan(&reportedPerms, &heldPerms); err != nil {
		t.Fatalf("counting permissions: %v", err)
	}
	if reportedPerms < heldPerms {
		t.Errorf("collapsed to a role granting %d permissions when one granting %d was held",
			reportedPerms, heldPerms)
	}
}

// TestSessionUpdateChangesOnlyWhatWasSent covers the reason clearing is a
// separate flag. A client correcting a start date must not blank the finish
// date it did not mention, and removing a mistyped date must be possible at
// all, which one nullable field cannot express.
func TestSessionUpdateChangesOnlyWhatWasSent(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewReadingSessionRepo(pool)

	var userID, bookID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no users: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM books LIMIT 1`).Scan(&bookID); err != nil {
		t.Skipf("no books: %v", err)
	}

	started, finished := "2026-01-02T00:00:00Z", "2026-02-03T00:00:00Z"
	s, err := repo.Create(ctx, CreateSessionInput{
		UserID: userID, BookID: bookID,
		StartedAt: &started, FinishedAt: &finished, Status: "finished",
	})
	if err != nil {
		t.Fatalf("creating a session: %v", err)
	}
	defer func() { _ = repo.Delete(ctx, s.ID) }()

	// Correcting the start date leaves the finish date alone.
	newStart := "2026-01-05T00:00:00Z"
	got, err := repo.Update(ctx, s.ID, UpdateSessionInput{StartedAt: &newStart})
	if err != nil {
		t.Fatalf("updating the start date: %v", err)
	}
	// Compared in UTC: the value is stored as an instant, and formatting it in
	// the machine's zone turns midnight UTC into the previous evening.
	if got.StartedAt == nil || got.StartedAt.UTC().Format("2006-01-02") != "2026-01-05" {
		t.Errorf("started_at = %v, want the corrected date", got.StartedAt)
	}
	if got.FinishedAt == nil {
		t.Error("correcting the start date cleared the finish date")
	}
	if got.Status != "finished" {
		t.Errorf("status = %q, want it untouched", got.Status)
	}

	// Clearing is a separate request, and it has to actually clear.
	got, err = repo.Update(ctx, s.ID, UpdateSessionInput{ClearFinished: true})
	if err != nil {
		t.Fatalf("clearing the finish date: %v", err)
	}
	if got.FinishedAt != nil {
		t.Errorf("finished_at = %v after an explicit clear, want nil", got.FinishedAt)
	}
	if got.StartedAt == nil {
		t.Error("clearing the finish date also cleared the start date")
	}
}

// TestSessionUpdateKeepsPageProgressHonest covers the rule holding after an
// edit rather than only at create time. A page number is meaningless without
// knowing which printing it counts, so clearing the edition on a session that
// counts pages has to be refused.
func TestSessionUpdateKeepsPageProgressHonest(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewReadingSessionRepo(pool)

	var userID, bookID, editionID uuid.UUID
	err := pool.QueryRow(ctx,
		`SELECT be.book_id, be.id FROM book_editions be LIMIT 1`).Scan(&bookID, &editionID)
	if err != nil {
		t.Skipf("no editions: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no users: %v", err)
	}

	pages := 200.0
	s, err := repo.Create(ctx, CreateSessionInput{
		UserID: userID, BookID: bookID, EditionID: &editionID,
		Status: "reading", ProgressUnit: "page", ProgressValue: &pages,
	})
	if err != nil {
		t.Fatalf("creating a page-progress session: %v", err)
	}
	defer func() { _ = repo.Delete(ctx, s.ID) }()

	if _, err := repo.Update(ctx, s.ID, UpdateSessionInput{ClearEdition: true}); !errors.Is(err, ErrPageNeedsEdition) {
		t.Errorf("clearing the edition on a page-counting session returned %v, want ErrPageNeedsEdition", err)
	}

	// The session is unchanged after the refusal.
	after, err := repo.FindByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("re-reading: %v", err)
	}
	if after.EditionID == nil {
		t.Error("the refused update cleared the edition anyway")
	}
}
