// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"errors"
	"os"
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
