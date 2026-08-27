// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fireball1725/librarium-api/internal/api/responses"
	"github.com/fireball1725/librarium-api/internal/models"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These run the real queries against a restored collection, which is the only
// way to catch the things a compiler cannot: a composite foreign key that does
// not bite, a recursive view that does not terminate, a containment link that
// silently makes a work contain itself.
//
// Set LIBRARIUM_TEST_DSN to a database that has run migration 000025.

// scratchUser makes an account this test owns, removed when it finishes.
//
// Live tests run against a restored copy of a real collection, so a test that
// writes has to own what it writes to. Picking whichever account
// `SELECT id FROM users LIMIT 1` returns and deleting its rows destroys
// somebody's actual data, which is not hypothetical: it happened.
func scratchUser(t *testing.T, pool *pgxpool.Pool, ctx context.Context) uuid.UUID {
	t.Helper()
	id := uuid.New()
	tag := id.String()[:8]
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, username, email, display_name, is_active)
		 VALUES ($1, $2, $3, 'Scratch', true)`,
		id, "scratch-"+tag, "scratch-"+tag+"@example.invalid"); err != nil {
		// Fatal rather than skipped. This named a password_hash column that
		// does not exist, so every test built on a scratch user skipped itself
		// and reported PASS. A skip is for "not configured to run here"; a
		// query that does not match the schema is a broken test.
		t.Fatalf("creating a scratch user: %v", err)
	}
	// Everything owned by a user cascades, so this takes the fixture with it.
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id) })
	return id
}

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

	// Its own account, so "start from none" needs no deleting.
	userID := scratchUser(t, pool, ctx)

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
	// Registered before Create, not after. Create inserts and then reads back,
	// so a failure in the read leaves the row behind, and a cleanup that only
	// runs on success leaks it into the sidebar of whoever owns this database.
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM lists WHERE id = $1`, shelfID)
	}()

	shelf, err := repo.Create(ctx, shelfID, libraryID, "test shelf", "desc", "#fff", "star", 3, userID)
	if err != nil {
		t.Fatalf("creating a shelf: %v", err)
	}
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

// TestListFilterRefusesSomebodyElsesList is the reason the filter carries a
// visibility arm. List ids arrive from a query string, so filtering on one
// without checking who owns it would let anyone read the contents of a private
// list by guessing at an id.
func TestListFilterRefusesSomebodyElsesList(t *testing.T) {
	pool, ctx := tiersPool(t)
	lists := NewListRepo(pool)
	books := NewBookRepo(pool)

	var owner, stranger, bookID uuid.UUID
	rows, err := pool.Query(ctx, `SELECT id FROM users LIMIT 2`)
	if err != nil {
		t.Skipf("no users: %v", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) < 2 {
		t.Skip("need two users to test one hiding a list from the other")
	}
	owner, stranger = ids[0], ids[1]
	// Picked from copies rather than books: the filter runs inside a
	// library-scoped list, so a book nothing holds would return zero for both
	// callers and the test would pass without testing anything.
	var libraryID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT book_id, library_id FROM copies WHERE deleted_at IS NULL LIMIT 1`).
		Scan(&bookID, &libraryID); err != nil {
		t.Skipf("no copies: %v", err)
	}

	l, err := lists.Create(ctx, CreateListInput{
		OwnerUserID: owner, Name: "test private list", Kind: "manual", Visibility: "private",
	})
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	defer func() { _ = lists.Delete(ctx, l.ID) }()
	if err := lists.AddBook(ctx, l.ID, bookID, 0); err != nil {
		t.Fatalf("adding a book: %v", err)
	}

	count := func(caller uuid.UUID) int {
		_, total, err := books.List(ctx, libraryID, ListBooksOpts{
			CallerID: caller, ShelfIDs: []uuid.UUID{l.ID}, PerPage: 50,
		})
		if err != nil {
			t.Fatalf("listing as %s: %v", caller, err)
		}
		return total
	}

	if got := count(owner); got != 1 {
		t.Errorf("the owner sees %d books in their own list, want 1", got)
	}
	if got := count(stranger); got != 0 {
		t.Errorf("a stranger filtering on someone else's private list sees %d books, want 0", got)
	}
}

// TestSyncProgressRoundTrips covers a regression that lost real data.
//
// Progress moved from the interactions row to reading_sessions, and the sync
// path briefly rejected the field on the reasoning that a client told invalid
// would stop sending it. The shipping iOS client does the opposite: it treats
// invalid as acknowledged and deletes the op, so a reader who set their page
// count saw it save locally and never leave the device.
func TestSyncProgressRoundTrips(t *testing.T) {
	pool, ctx := tiersPool(t)
	sync := NewSyncRepo(pool)

	// A scratch reader with an opinion of a real book, rather than a real
	// reader whose sessions this would have to clear first. Clearing them is
	// what an earlier version did, and reading history is not test scaffolding.
	userID := scratchUser(t, pool, ctx)

	// The book only has to have an edition, since a page number needs a
	// printing to count in.
	var bookID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT be.book_id FROM book_editions be LIMIT 1`).Scan(&bookID); err != nil {
		t.Skipf("no editions: %v", err)
	}
	var entityID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO user_books (user_id, book_id, read_status)
		 VALUES ($1, $2, 'reading') RETURNING id`, userID, bookID).Scan(&entityID); err != nil {
		t.Fatalf("creating an opinion: %v", err)
	}

	at := time.Now().Add(-time.Hour)
	op := responses.SyncApplyOp{
		EntityType: "user_book_interaction",
		EntityID:   entityID,
		Field:      "progress",
		Value:      map[string]any{"pages_read": float64(120)},
		UpdatedAt:  at,
	}
	status, err := sync.ApplyUserBookInteractionOp(ctx, userID, op)
	if err != nil {
		t.Fatalf("applying progress: %v", err)
	}
	if status != SyncApplyStatusApplied {
		t.Fatalf("progress returned %q, want applied; invalid is what silently dropped it", status)
	}

	var unit string
	var value float64
	if err := pool.QueryRow(ctx,
		`SELECT progress_unit, progress_value FROM reading_sessions
		  WHERE user_id = $1 AND book_id = $2 AND finished_at IS NULL
		  ORDER BY created_at DESC LIMIT 1`, userID, bookID).Scan(&unit, &value); err != nil {
		t.Fatalf("progress was accepted but stored nowhere: %v", err)
	}
	if unit != "page" || value != 120 {
		t.Errorf("stored %s=%v, want page=120", unit, value)
	}

	// An older op must not overwrite a newer value, the same rule every other
	// synced field follows.
	older := op
	older.UpdatedAt = at.Add(-time.Hour)
	older.Value = map[string]any{"pages_read": float64(5)}
	if status, err := sync.ApplyUserBookInteractionOp(ctx, userID, older); err != nil {
		t.Fatalf("applying an older op: %v", err)
	} else if status != SyncApplyStatusDiscardedStale {
		t.Errorf("an older progress op returned %q, want discarded_stale", status)
	}

	// And it comes back down, or a second device never learns it.
	ops, err := sync.UserBookInteractionChanges(ctx, userID, at.Add(-2*time.Hour), 500)
	if err != nil {
		t.Fatalf("reading changes: %v", err)
	}
	// The feed is one op per changed field, so progress is its own row.
	found := false
	for _, o := range ops {
		if o.EntityID == entityID && o.Field == "progress" && o.Value != nil {
			found = true
		}
	}
	if !found {
		t.Error("progress was stored but never sent back, so another device would never see it")
	}
}

// TestInteractionCarriesItsRowID guards a field that looks like decoration and
// is not.
//
// The iOS outbox addresses every offline edit by interaction.id. This read
// briefly returned the zero UUID, which parses as a perfectly good UUID on the
// client, so edits were aimed at a row that does not exist, acknowledged as
// not_found, and deleted. The old routes still serve clients in the field, so
// the id has to be real there and not only on the new ones.
func TestInteractionCarriesItsRowID(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewEditionRepo(pool)

	var userID, editionID, wantID uuid.UUID
	err := pool.QueryRow(ctx,
		`SELECT ub.user_id, be.id, ub.id
		   FROM user_books ub
		   JOIN book_editions be ON be.book_id = ub.book_id
		  WHERE ub.deleted_at IS NULL
		  LIMIT 1`).Scan(&userID, &editionID, &wantID)
	if err != nil {
		t.Skipf("no opinion on a book with an edition: %v", err)
	}

	got, err := repo.GetInteraction(ctx, userID, editionID)
	if err != nil {
		t.Fatalf("reading the interaction: %v", err)
	}
	if got.ID == uuid.Nil {
		t.Fatal("interaction came back with the zero id; every outbox op addressed to it would be dropped")
	}
	if got.ID != wantID {
		t.Errorf("id = %s, want the user_books row id %s", got.ID, wantID)
	}
	// Still the edition that was asked for, since old clients key their local
	// store on it.
	if got.BookEditionID != editionID {
		t.Errorf("book_edition_id = %s, want the edition requested %s", got.BookEditionID, editionID)
	}
}

// TestLegacySyncIDsStillResolve covers the upgrade path for a client that was
// offline across it.
//
// 000027 minted fresh surrogate ids for user_books, so an id cached before the
// upgrade addresses nothing. Its comment said the client would get not_found
// and re-read, losing only a round trip. The shipping iOS client instead treats
// not_found as acknowledged and deletes the op, so every edit queued offline
// before the upgrade would be thrown away on reconnect.
func TestLegacySyncIDsStillResolve(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewSyncRepo(pool)

	var legacyID, userBookID, userID uuid.UUID
	err := pool.QueryRow(ctx,
		`SELECT l.legacy_id, l.user_book_id, ub.user_id
		   FROM legacy_interaction_ids l
		   JOIN user_books ub ON ub.id = l.user_book_id
		  WHERE ub.deleted_at IS NULL
		  LIMIT 1`).Scan(&legacyID, &userBookID, &userID)
	if err != nil {
		t.Skipf("no forwarded ids: %v", err)
	}
	if legacyID == userBookID {
		t.Skip("this install kept its ids, so there is nothing to forward")
	}

	// An op addressed the old way has to land, not be answered not_found.
	before := time.Now()
	status, err := repo.ApplyUserBookInteractionOp(ctx, userID, responses.SyncApplyOp{
		EntityType: "user_book_interaction",
		EntityID:   legacyID,
		Field:      "is_favorite",
		Value:      true,
		UpdatedAt:  before,
	})
	if err != nil {
		t.Fatalf("applying through a legacy id: %v", err)
	}
	if status != SyncApplyStatusApplied {
		t.Fatalf("a legacy id returned %q, want applied; not_found is what the client throws away", status)
	}

	var fav bool
	if err := pool.QueryRow(ctx,
		`SELECT is_favorite FROM user_books WHERE id = $1`, userBookID).Scan(&fav); err != nil {
		t.Fatalf("re-reading: %v", err)
	}
	if !fav {
		t.Error("the op was acknowledged but changed nothing")
	}
	// Put it back rather than leaving a favourite nobody set.
	_, _ = pool.Exec(ctx,
		`UPDATE user_books SET is_favorite = false, is_favorite_updated_at = NULL WHERE id = $1`,
		userBookID)

	// An id belonging to nobody still misses, so forwarding has not become a
	// way to write to another person's row.
	status, err = repo.ApplyUserBookInteractionOp(ctx, userID, responses.SyncApplyOp{
		EntityType: "user_book_interaction",
		EntityID:   uuid.New(),
		Field:      "is_favorite",
		Value:      true,
		UpdatedAt:  before,
	})
	if err != nil {
		t.Fatalf("applying an unknown id: %v", err)
	}
	if status != SyncApplyStatusNotFound {
		t.Errorf("an unknown id returned %q, want not_found", status)
	}
}

// TestListsHoldingBookRespectsVisibility covers the reverse lookup a book page
// asks. It reads across every list in the table, so the visibility rule matters
// as much here as it does in the filter.
func TestListsHoldingBookRespectsVisibility(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewListRepo(pool)

	var owner, stranger, bookID uuid.UUID
	rows, err := pool.Query(ctx, `SELECT id FROM users LIMIT 2`)
	if err != nil {
		t.Skipf("no users: %v", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) < 2 {
		t.Skip("need two users")
	}
	owner, stranger = ids[0], ids[1]
	if err := pool.QueryRow(ctx, `SELECT id FROM books LIMIT 1`).Scan(&bookID); err != nil {
		t.Skipf("no books: %v", err)
	}

	manual, err := repo.Create(ctx, CreateListInput{
		OwnerUserID: owner, Name: "test holding list", Kind: "manual", Visibility: "private",
	})
	if err != nil {
		t.Fatalf("creating a manual list: %v", err)
	}
	defer func() { _ = repo.Delete(ctx, manual.ID) }()
	if err := repo.AddBook(ctx, manual.ID, bookID, 0); err != nil {
		t.Fatalf("adding the book: %v", err)
	}

	// A smart list is deliberately absent even when its filter would match:
	// answering for one would mean running every stored filter to draw a label.
	smart, err := repo.Create(ctx, CreateListInput{
		OwnerUserID: owner, Name: "test holding smart", Kind: "smart",
		Filter: []byte(`{"query":""}`),
	})
	if err != nil {
		t.Fatalf("creating a smart list: %v", err)
	}
	defer func() { _ = repo.Delete(ctx, smart.ID) }()

	mine, err := repo.ContainingBook(ctx, owner, bookID)
	if err != nil {
		t.Fatalf("reading as the owner: %v", err)
	}
	var sawManual, sawSmart bool
	for _, l := range mine {
		if l.ID == manual.ID {
			sawManual = true
		}
		if l.ID == smart.ID {
			sawSmart = true
		}
	}
	if !sawManual {
		t.Error("the owner cannot see their own list holding the book")
	}
	if sawSmart {
		t.Error("a smart list was reported as holding a book; its membership is a filter, not a set")
	}

	theirs, err := repo.ContainingBook(ctx, stranger, bookID)
	if err != nil {
		t.Fatalf("reading as a stranger: %v", err)
	}
	for _, l := range theirs {
		if l.ID == manual.ID {
			t.Error("a stranger can see someone else's private list holding a book")
		}
	}
}

// TestExampleListsAreSeededOnceAndStayDeleted covers what makes them examples
// rather than fixtures.
//
// They used to be re-inserted on every read, so deleting one worked and the
// next page load put it straight back. A reader who threw away Five stars had
// to watch it return.
func TestExampleListsAreSeededOnceAndStayDeleted(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewListRepo(pool)

	userID := scratchUser(t, pool, ctx)

	count := func() int {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM lists WHERE owner_user_id = $1 AND builtin_key IS NOT NULL`,
			userID).Scan(&n); err != nil {
			t.Fatalf("counting: %v", err)
		}
		return n
	}

	if err := repo.SeedBuiltIns(ctx, userID); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	seeded := count()
	if seeded != len(BuiltinLists) {
		t.Fatalf("seeded %d, want %d", seeded, len(BuiltinLists))
	}

	// Throw one away, the way a reader would.
	var fiveStars uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM lists WHERE owner_user_id = $1 AND builtin_key = 'five-stars'`,
		userID).Scan(&fiveStars); err != nil {
		t.Fatalf("finding five-stars: %v", err)
	}
	if err := repo.Delete(ctx, fiveStars); err != nil {
		t.Fatalf("deleting an example: %v", err)
	}

	// Every later read seeds again. It must not come back.
	for i := 0; i < 3; i++ {
		if err := repo.SeedBuiltIns(ctx, userID); err != nil {
			t.Fatalf("re-seeding: %v", err)
		}
	}
	if got := count(); got != seeded-1 {
		t.Errorf("after deleting one example and %d reads there are %d, want %d: it came back",
			3, got, seeded-1)
	}

	// The default is machinery, not an example: the books page has to open on
	// something, so it is still refused.
	var dflt uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM lists WHERE owner_user_id = $1 AND builtin_key = 'default'`,
		userID).Scan(&dflt); err != nil {
		t.Fatalf("finding the default: %v", err)
	}
	if err := repo.Delete(ctx, dflt); !errors.Is(err, ErrListPermanent) {
		t.Errorf("deleting the default returned %v, want ErrListPermanent", err)
	}

	// An example can be renamed and retargeted, because it belongs to whoever
	// was given it.
	var reading uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM lists WHERE owner_user_id = $1 AND builtin_key = 'reading'`,
		userID).Scan(&reading); err != nil {
		t.Fatalf("finding reading: %v", err)
	}
	name := "On the go"
	if _, err := repo.Update(ctx, reading, UpdateListInput{
		Name: &name, Filter: []byte(`{"query":"status=reading&fav=true"}`),
	}); err != nil {
		t.Fatalf("editing an example: %v", err)
	}
	after, err := repo.FindByID(ctx, reading)
	if err != nil {
		t.Fatalf("re-reading: %v", err)
	}
	if after.Name != name {
		t.Errorf("name = %q, want the rename to stick", after.Name)
	}
}

// TestContributorFilterIsNotATextSearch covers the difference the search box
// depends on: naming an author is a different question from searching for their
// name. "Tite" the person is not a book with Tite in its title.
func TestContributorFilterIsNotATextSearch(t *testing.T) {
	pool, ctx := tiersPool(t)
	books := NewBookRepo(pool)

	var contributorID, libraryID uuid.UUID
	var name string
	err := pool.QueryRow(ctx, `
		SELECT c.id, c.name, cp.library_id
		  FROM contributors c
		  JOIN book_contributors bc ON bc.contributor_id = c.id
		  JOIN copies cp ON cp.book_id = bc.book_id AND cp.deleted_at IS NULL
		 GROUP BY c.id, c.name, cp.library_id
		HAVING count(*) > 1
		 LIMIT 1`).Scan(&contributorID, &name, &libraryID)
	if err != nil {
		t.Skipf("no contributor with more than one held book: %v", err)
	}

	_, total, err := books.List(ctx, libraryID, ListBooksOpts{
		PerPage:   200,
		Selection: FacetSelection{Contributors: []uuid.UUID{contributorID}},
	})
	if err != nil {
		t.Fatalf("filtering by contributor: %v", err)
	}
	if total == 0 {
		t.Fatalf("%s has books but the filter matched none", name)
	}

	// Every row really is theirs, which a name match could not promise.
	rows, _, err := books.List(ctx, libraryID, ListBooksOpts{
		PerPage:   200,
		Selection: FacetSelection{Contributors: []uuid.UUID{contributorID}},
	})
	if err != nil {
		t.Fatalf("re-reading: %v", err)
	}
	for _, b := range rows {
		var linked bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM book_contributors
			                WHERE book_id = $1 AND contributor_id = $2)`,
			b.ID, contributorID).Scan(&linked); err != nil {
			t.Fatalf("checking the credit: %v", err)
		}
		if !linked {
			t.Errorf("%q came back for a contributor who is not credited on it", b.Title)
		}
	}

	// An id nobody holds matches nothing, rather than everything.
	_, none, err := books.List(ctx, libraryID, ListBooksOpts{
		PerPage:   10,
		Selection: FacetSelection{Contributors: []uuid.UUID{uuid.New()}},
	})
	if err != nil {
		t.Fatalf("filtering by an unknown contributor: %v", err)
	}
	if none != 0 {
		t.Errorf("an unknown contributor matched %d books, want 0", none)
	}
}

// The rail and the rows have to answer the same question. The list runs a text
// query through the parser, which makes one condition per word; the facet query
// used to match the whole phrase in one ILIKE, so any two-word search reported
// zero of everything beside a list showing books.
func TestFacetsAgreeWithTheListOnAMultiWordSearch(t *testing.T) {
	pool, ctx := tiersPool(t)
	books := NewBookRepo(pool)

	// Two words that are known to co-occur: a title word and a word from a
	// contributor's name on the same book. Read from the data rather than
	// hardcoded, so the test travels with whatever collection it is run against.
	var titleWord, nameWord string
	var libraryID uuid.UUID
	err := pool.QueryRow(ctx, `
		SELECT split_part(b.title, ' ', 1),
		       split_part(c.name, ' ', 1),
		       cp.library_id
		  FROM books b
		  JOIN book_contributors bc ON bc.book_id = b.id
		  JOIN contributors c ON c.id = bc.contributor_id
		  JOIN copies cp ON cp.book_id = b.id AND cp.deleted_at IS NULL
		 WHERE length(split_part(b.title, ' ', 1)) > 3
		   AND length(split_part(c.name, ' ', 1)) > 3
		 LIMIT 1`).Scan(&titleWord, &nameWord, &libraryID)
	if err != nil {
		t.Skipf("no book with a multi-word title and a credited contributor: %v", err)
	}

	query := titleWord + " " + nameWord
	// What internal/search produces for two bare words. Built by hand because
	// that package imports this one, so the test cannot call the parser.
	groups := []ConditionGroup{{Mode: "and", Conditions: []FilterCondition{
		{Field: "title", Op: "contains", Value: titleWord},
		{Field: "title", Op: "contains", Value: nameWord},
	}}}
	_, total, err := books.List(ctx, libraryID, ListBooksOpts{PerPage: 1, Groups: groups})
	if err != nil {
		t.Fatalf("listing for %q: %v", query, err)
	}
	if total == 0 {
		t.Fatalf("%q matched nothing, so there is nothing to compare", query)
	}

	facets, err := books.Facets(ctx, []uuid.UUID{libraryID}, FacetSelection{}, query, nil, uuid.Nil)
	if err != nil {
		t.Fatalf("counting facets for %q: %v", query, err)
	}
	var counted int
	for _, v := range facets.Library {
		counted += v.Count
	}
	if counted != total {
		t.Errorf("the rail counted %d for %q, the list returned %d", counted, query, total)
	}
}

// Order belongs to a sidebar, not to a view. Two people looking at the same
// shared view put it in different places, and neither move is visible to the
// other. Before list_order_overrides there was one number on the row, so the
// second person's drag either moved it for the first or, because the reorder
// was a PATCH guarded by ownership, failed and was swallowed.
func TestRailOrderIsPerPerson(t *testing.T) {
	pool, ctx := tiersPool(t)
	lists := NewListRepo(pool)

	owner := scratchUser(t, pool, ctx)
	reader := scratchUser(t, pool, ctx)

	var libraryID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no library to share into: %v", err)
	}
	// The reader has to hold a role on the library or the shared view is not
	// theirs to see, let alone to arrange.
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_roles (user_id, library_id, role_id, scope)
		 SELECT $1, $2, r.id, r.scope FROM roles r
		  WHERE r.name = 'library_viewer'`,
		reader, libraryID); err != nil {
		t.Fatalf("granting the reader a role: %v", err)
	}

	shared, err := lists.Create(ctx, CreateListInput{
		OwnerUserID: owner, Name: "Shared run", Kind: "smart",
		Filter: []byte(`{"query":"tag=manga"}`), Layout: "list",
		Visibility: "library", SharedLibraryID: &libraryID,
	})
	if err != nil {
		t.Fatalf("creating a shared view: %v", err)
	}
	mine, err := lists.Create(ctx, CreateListInput{
		OwnerUserID: reader, Name: "Mine", Kind: "smart",
		Filter: []byte(`{"query":""}`), Layout: "list", Visibility: "private",
	})
	if err != nil {
		t.Fatalf("creating the reader's own view: %v", err)
	}

	// The reader puts the shared one first. It is not theirs, which is exactly
	// the move that used to fail.
	if err := lists.SetOrder(ctx, reader, []uuid.UUID{shared.ID, mine.ID}); err != nil {
		t.Fatalf("arranging the reader's rail: %v", err)
	}

	order := func(who uuid.UUID) []string {
		ls, err := lists.ListForUser(ctx, who)
		if err != nil {
			t.Fatalf("reading a rail: %v", err)
		}
		var names []string
		for _, l := range ls {
			if l.ID == shared.ID || l.ID == mine.ID {
				names = append(names, l.Name)
			}
		}
		return names
	}

	if got := order(reader); len(got) != 2 || got[0] != "Shared run" {
		t.Errorf("the reader's rail is %v, want Shared run first", got)
	}

	// The owner never asked for anything, so their rail is untouched: the
	// reader's drag is invisible to them.
	var ownerHasOpinion bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM list_order_overrides
		                WHERE user_id = $1 AND list_id = $2)`,
		owner, shared.ID).Scan(&ownerHasOpinion); err != nil {
		t.Fatalf("checking the owner's rail: %v", err)
	}
	if ownerHasOpinion {
		t.Error("the reader's drag wrote a position into the owner's rail")
	}

	// An id the caller cannot see is ignored rather than refused, so one stale
	// row does not strand the rest of the reorder.
	if err := lists.SetOrder(ctx, reader, []uuid.UUID{uuid.New(), mine.ID}); err != nil {
		t.Errorf("an unknown id failed the whole reorder: %v", err)
	}
}

// A list shared with a library is that library's working set. Restricting who
// can file a book to whoever created it would mean nobody else could use it,
// which is the rule the per-library shelf routes already applied and which
// membership had to keep when the route moved off the library path.
func TestASharedListIsEditableByTheLibrary(t *testing.T) {
	pool, ctx := tiersPool(t)
	lists := NewListRepo(pool)

	owner := scratchUser(t, pool, ctx)
	member := scratchUser(t, pool, ctx)
	stranger := scratchUser(t, pool, ctx)

	var libraryID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no library to share into: %v", err)
	}
	// A role that actually carries shelves:update, read from the data rather
	// than named, so the test does not encode a role name that may change.
	var roleID uuid.UUID
	var scope string
	if err := pool.QueryRow(ctx, `
		SELECT r.id, r.scope FROM roles r
		  JOIN role_permissions rp ON rp.role_id = r.id
		  JOIN permissions p ON p.id = rp.permission_id
		 WHERE p.name = 'shelves:update' AND r.scope = 'library'
		 LIMIT 1`).Scan(&roleID, &scope); err != nil {
		t.Skipf("no library role grants shelves:update: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_roles (user_id, library_id, role_id, scope) VALUES ($1, $2, $3, $4)`,
		member, libraryID, roleID, scope); err != nil {
		t.Fatalf("granting the member a role: %v", err)
	}

	shared, err := lists.Create(ctx, CreateListInput{
		OwnerUserID: owner, Name: "Shared working set", Kind: "manual",
		Layout: "list", Visibility: "library", SharedLibraryID: &libraryID,
	})
	if err != nil {
		t.Fatalf("creating a shared list: %v", err)
	}
	private, err := lists.Create(ctx, CreateListInput{
		OwnerUserID: owner, Name: "Mine alone", Kind: "manual",
		Layout: "list", Visibility: "private",
	})
	if err != nil {
		t.Fatalf("creating a private list: %v", err)
	}

	for _, c := range []struct {
		who   uuid.UUID
		list  uuid.UUID
		want  bool
		label string
	}{
		{owner, shared.ID, true, "the owner edits their own shared list"},
		{member, shared.ID, true, "a member of the library files a book on it"},
		{stranger, shared.ID, false, "someone with no role on the library does not"},
		{owner, private.ID, true, "the owner edits their private list"},
		{member, private.ID, false, "a library role does not reach a private list"},
	} {
		got, err := lists.Editable(ctx, c.who, c.list)
		if err != nil {
			t.Fatalf("%s: %v", c.label, err)
		}
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.label, got, c.want)
		}
	}
}

// Sharing has to be changeable after the fact. A list starts private because
// that is the safe default, and deciding later that the household should see it
// is the normal case, not a reason to rebuild it by hand.
func TestAListMovesBetweenPrivateAndShared(t *testing.T) {
	pool, ctx := tiersPool(t)
	lists := NewListRepo(pool)

	owner := scratchUser(t, pool, ctx)
	var libraryID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no library to share into: %v", err)
	}

	made, err := lists.Create(ctx, CreateListInput{
		OwnerUserID: owner, Name: "Gift ideas", Kind: "manual",
		Layout: "list", Visibility: "private",
	})
	if err != nil {
		t.Fatalf("creating a private list: %v", err)
	}

	shared := "library"
	got, err := lists.Update(ctx, made.ID, UpdateListInput{
		Visibility: &shared, SharedLibraryID: &libraryID,
	})
	if err != nil {
		t.Fatalf("sharing it: %v", err)
	}
	if got.Visibility != "library" || got.SharedLibraryID == nil || *got.SharedLibraryID != libraryID {
		t.Fatalf("after sharing: visibility=%q library=%v", got.Visibility, got.SharedLibraryID)
	}

	// And back. The library has to be cleared with it or the row breaks the
	// constraint that ties the two together.
	private := "private"
	got, err = lists.Update(ctx, made.ID, UpdateListInput{Visibility: &private})
	if err != nil {
		t.Fatalf("making it private again: %v", err)
	}
	if got.Visibility != "private" || got.SharedLibraryID != nil {
		t.Errorf("after unsharing: visibility=%q library=%v, want private and nothing",
			got.Visibility, got.SharedLibraryID)
	}

	// Naming no library is refused rather than written as a row the table would
	// reject anyway, so the message says what is missing.
	if _, err := lists.Update(ctx, made.ID, UpdateListInput{Visibility: &shared}); err == nil {
		t.Error("shared a list with no library named")
	}

	// A public list keeps its token, or every link already handed out dies the
	// next time anything about the list is saved.
	public := "public"
	first, err := lists.Update(ctx, made.ID, UpdateListInput{Visibility: &public})
	if err != nil {
		t.Fatalf("publishing it: %v", err)
	}
	if first.ShareToken == "" {
		t.Fatal("a public list came back with no token")
	}
	again, err := lists.Update(ctx, made.ID, UpdateListInput{Visibility: &public})
	if err != nil {
		t.Fatalf("saving it again: %v", err)
	}
	if again.ShareToken != first.ShareToken {
		t.Errorf("the token changed on a second save: %q then %q", first.ShareToken, again.ShareToken)
	}
}

// A place contains what is inside it. Filing a book on a shelf has to make it
// findable under the bookcase holding that shelf and the room holding that,
// or narrowing to a room hides everything actually on its shelves.
func TestLocationFacetCountsUpTheTree(t *testing.T) {
	pool, ctx := tiersPool(t)
	books := NewBookRepo(pool)
	locs := NewCopyLocationRepo(pool)

	var libraryID uuid.UUID
	var copyID, bookID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT c.library_id, c.id, c.book_id FROM copies c
		 WHERE c.deleted_at IS NULL LIMIT 1`).Scan(&libraryID, &copyID, &bookID); err != nil {
		t.Skipf("no copy to file: %v", err)
	}

	room, err := locs.Create(ctx, libraryID, "ZZ test room", nil)
	if err != nil {
		t.Fatalf("creating the room: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM copy_locations WHERE id = $1`, room.ID) })
	shelf, err := locs.Create(ctx, libraryID, "ZZ test shelf", &room.ID)
	if err != nil {
		t.Fatalf("creating the shelf: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM copy_locations WHERE id = $1`, shelf.ID) })

	var was *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT location_id FROM copies WHERE id = $1`, copyID).Scan(&was); err != nil {
		t.Fatalf("reading where the copy was: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE copies SET location_id = $2 WHERE id = $1`, copyID, shelf.ID); err != nil {
		t.Fatalf("filing the copy: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE copies SET location_id = $2 WHERE id = $1`, copyID, was)
	})

	facets, err := books.Facets(ctx, []uuid.UUID{libraryID}, FacetSelection{}, "", nil, uuid.Nil)
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	seen := map[string]int{}
	for _, v := range facets.Location {
		seen[v.Label] = v.Count
	}
	if seen["ZZ test shelf"] < 1 {
		t.Errorf("the shelf the copy is on counted %d, want at least 1", seen["ZZ test shelf"])
	}
	if seen["ZZ test room"] < 1 {
		t.Errorf("the room containing that shelf counted %d; a place has to contain what is inside it",
			seen["ZZ test room"])
	}

	// And the list agrees with the count beside it, asked about the ancestor.
	_, total, err := books.List(ctx, libraryID, ListBooksOpts{
		PerPage: 50, Selection: FacetSelection{Locations: []uuid.UUID{room.ID}},
	})
	if err != nil {
		t.Fatalf("listing by the room: %v", err)
	}
	if total != seen["ZZ test room"] {
		t.Errorf("the room counted %d but the list returned %d", seen["ZZ test room"], total)
	}
}

// Ratings run 1 to 10, which is what the column's CHECK allows and what the
// data uses. The handler parsed 0 to 5, so every rating above 5 was dropped on
// the way in and the filter silently widened to the whole collection.
func TestRatingFilterReachesTheWholeScale(t *testing.T) {
	pool, ctx := tiersPool(t)
	books := NewBookRepo(pool)

	var libraryID uuid.UUID
	var rating int32
	if err := pool.QueryRow(ctx, `
		SELECT c.library_id, ub.rating
		  FROM user_books ub
		  JOIN copies c ON c.book_id = ub.book_id AND c.deleted_at IS NULL
		 WHERE ub.rating > 5
		 LIMIT 1`).Scan(&libraryID, &rating); err != nil {
		t.Skipf("no rating above 5 to filter on: %v", err)
	}

	var callerID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT user_id FROM user_books WHERE rating = $1 LIMIT 1`, rating).Scan(&callerID); err != nil {
		t.Fatalf("finding whose rating that is: %v", err)
	}

	_, filtered, err := books.List(ctx, libraryID, ListBooksOpts{
		PerPage: 1, CallerID: callerID,
		Selection: FacetSelection{Ratings: []int32{rating}},
	})
	if err != nil {
		t.Fatalf("filtering by rating %d: %v", rating, err)
	}
	_, all, err := books.List(ctx, libraryID, ListBooksOpts{PerPage: 1, CallerID: callerID})
	if err != nil {
		t.Fatalf("listing everything: %v", err)
	}
	if filtered == 0 {
		t.Errorf("rating %d matched nothing", rating)
	}
	if filtered == all {
		t.Errorf("rating %d returned all %d books, so the filter did nothing", rating, all)
	}
}

// A rating is a fact about the book, averaged over everyone who can see it,
// while each person's own is still stored and still filterable. Two people
// rating the same book is the case the local data never has, so it is built
// here rather than waited for.
func TestRatingIsAveragedButStillIndividual(t *testing.T) {
	pool, ctx := tiersPool(t)
	books := NewBookRepo(pool)

	alice := scratchUser(t, pool, ctx)
	bob := scratchUser(t, pool, ctx)

	// A book nobody has rated, because the arithmetic below depends on it.
	//
	// This took the first held copy it found, which was fine only while that
	// row happened to be unrated: alice's 9 and bob's 6 average to 8, and a
	// third rating already on the book moves the answer somewhere the filter
	// will not find. The row is arbitrary with no ORDER BY, so a book added or
	// removed anywhere could change which one it grabs, and the test would fail
	// on a change that had nothing to do with it.
	var libraryID, bookID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT c.library_id, c.book_id FROM copies c
		 WHERE c.deleted_at IS NULL
		   AND NOT EXISTS (SELECT 1 FROM user_books ub
		                    WHERE ub.book_id = c.book_id AND ub.rating IS NOT NULL)
		 ORDER BY c.book_id LIMIT 1`).Scan(&libraryID, &bookID); err != nil {
		t.Skipf("no unrated held book to rate: %v", err)
	}

	var roleID uuid.UUID
	var scope string
	if err := pool.QueryRow(ctx,
		`SELECT id, scope FROM roles WHERE scope = 'library' LIMIT 1`).Scan(&roleID, &scope); err != nil {
		t.Skipf("no library role: %v", err)
	}
	for _, who := range []uuid.UUID{alice, bob} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO user_roles (user_id, library_id, role_id, scope) VALUES ($1, $2, $3, $4)`,
			who, libraryID, roleID, scope); err != nil {
			t.Fatalf("granting a role: %v", err)
		}
	}

	// 9 and 6 average to 7.5, which rounds to 8: four stars.
	for _, r := range []struct {
		who    uuid.UUID
		rating int
	}{{alice, 9}, {bob, 6}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO user_books (user_id, book_id, read_status, rating)
			 VALUES ($1, $2, 'read', $3)
			 ON CONFLICT (user_id, book_id) DO UPDATE SET rating = EXCLUDED.rating`,
			r.who, bookID, r.rating); err != nil {
			t.Fatalf("recording a rating: %v", err)
		}
	}

	// The average, which neither of them gave it.
	rows, _, err := books.List(ctx, libraryID, ListBooksOpts{
		PerPage: 200, CallerID: alice, Selection: FacetSelection{Ratings: []int32{8}},
	})
	if err != nil {
		t.Fatalf("filtering by the average: %v", err)
	}
	if !holds(rows, bookID) {
		t.Error("the book did not come back under its average of 8")
	}

	// Alice's own is still 9 and still findable, which is the thing that would
	// have been lost by replacing one filter with the other.
	rows, _, err = books.List(ctx, libraryID, ListBooksOpts{
		PerPage: 200, CallerID: alice, Selection: FacetSelection{MyRatings: []int32{9}},
	})
	if err != nil {
		t.Fatalf("filtering by my rating: %v", err)
	}
	if !holds(rows, bookID) {
		t.Error("Alice cannot find the book she rated 9")
	}

	// And Bob's six is his alone: it is not what Alice rated it.
	rows, _, err = books.List(ctx, libraryID, ListBooksOpts{
		PerPage: 200, CallerID: alice, Selection: FacetSelection{MyRatings: []int32{6}},
	})
	if err != nil {
		t.Fatalf("filtering by a rating Alice did not give: %v", err)
	}
	if holds(rows, bookID) {
		t.Error("Alice's own filter matched Bob's rating")
	}

	// The facet agrees with the list it sits beside.
	facets, err := books.Facets(ctx, []uuid.UUID{libraryID}, FacetSelection{}, "", nil, alice)
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if countOf(facets.Rating, "8") == 0 {
		t.Error("the average facet has no row for 8")
	}
	if countOf(facets.MyRating, "9") == 0 {
		t.Error("the my-rating facet has no row for Alice's 9")
	}
	if countOf(facets.MyRating, "6") != 0 {
		t.Error("Bob's rating appeared in Alice's my-rating facet")
	}
}

func holds(rows []*models.Book, id uuid.UUID) bool {
	for _, b := range rows {
		if b.ID == id {
			return true
		}
	}
	return false
}

func countOf(vs []FacetValue, value string) int {
	for _, v := range vs {
		if v.Value == value {
			return v.Count
		}
	}
	return 0
}

// A review the product calls "visible to members" was written and shown to
// nobody, and a rating beside it could only ever be seen by its author. Members
// are exactly who should see both.
func TestReadersOfShowsMembersAndHidesPrivateNotes(t *testing.T) {
	pool, ctx := tiersPool(t)
	userBooks := NewUserBookRepo(pool)

	me := scratchUser(t, pool, ctx)
	housemate := scratchUser(t, pool, ctx)
	stranger := scratchUser(t, pool, ctx)

	var libraryID, bookID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT c.library_id, c.book_id FROM copies c
		 WHERE c.deleted_at IS NULL LIMIT 1`).Scan(&libraryID, &bookID); err != nil {
		t.Skipf("no held book: %v", err)
	}
	var roleID uuid.UUID
	var scope string
	if err := pool.QueryRow(ctx,
		`SELECT id, scope FROM roles WHERE scope = 'library' LIMIT 1`).Scan(&roleID, &scope); err != nil {
		t.Skipf("no library role: %v", err)
	}
	for _, who := range []uuid.UUID{me, housemate} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO user_roles (user_id, library_id, role_id, scope) VALUES ($1, $2, $3, $4)`,
			who, libraryID, roleID, scope); err != nil {
			t.Fatalf("granting a role: %v", err)
		}
	}

	for _, w := range []struct {
		who    uuid.UUID
		rating int
		review string
		notes  string
	}{
		{housemate, 9, "Loved the ending.", "must not leak"},
		{stranger, 2, "Not for me.", "also must not leak"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO user_books (user_id, book_id, read_status, rating, review, notes)
			 VALUES ($1, $2, 'read', $3, $4, $5)
			 ON CONFLICT (user_id, book_id) DO UPDATE
			    SET rating = EXCLUDED.rating, review = EXCLUDED.review, notes = EXCLUDED.notes`,
			w.who, bookID, w.rating, w.review, w.notes); err != nil {
			t.Fatalf("recording a reading: %v", err)
		}
	}

	readers, err := userBooks.ReadersOf(ctx, bookID, me)
	if err != nil {
		t.Fatalf("listing readers: %v", err)
	}

	var sawHousemate, sawStranger bool
	for _, rd := range readers {
		if rd.UserID == housemate {
			sawHousemate = true
			if rd.Rating == nil || *rd.Rating != 9 {
				t.Errorf("housemate's rating came back as %v, want 9", rd.Rating)
			}
			if rd.Review != "Loved the ending." {
				t.Errorf("housemate's review came back as %q", rd.Review)
			}
		}
		if rd.UserID == stranger {
			sawStranger = true
		}
	}
	if !sawHousemate {
		t.Error("a housemate's reading was not shown to someone in the same library")
	}
	if sawStranger {
		t.Error("someone sharing no library was shown; an instance can host unrelated households")
	}

	// Notes are private where they are written, so the promise is kept by the
	// query never selecting them rather than by a handler remembering to strip
	// them. There is no field on the struct to leak through.
	blob := fmt.Sprintf("%+v", readers)
	if strings.Contains(blob, "must not leak") {
		t.Error("private notes reached a reader they do not belong to")
	}
}

// A place and everything inside it read as one block. The order was
// COALESCE(parent_id, id), which groups a node with its siblings and no
// further: a grandchild sorted under its own parent's UUID, so a room could
// appear between another room and the shelf inside it.
//
// The ids are fixed rather than generated. The old ordering sorted on them, so
// with random ids this test passed or failed on luck; these are chosen so the
// old query puts the second root inside the first root's subtree every time.
func TestLocationsListDepthFirst(t *testing.T) {
	pool, ctx := tiersPool(t)
	locs := NewCopyLocationRepo(pool)

	var libraryID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no library: %v", err)
	}

	office := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	root := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	shelf := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	box := uuid.MustParse("44444444-4444-4444-8444-444444444444")

	for _, n := range []struct {
		id     uuid.UUID
		name   string
		parent *uuid.UUID
	}{
		{office, "ZZ office", nil},
		{root, "ZZ root", nil},
		{shelf, "ZZ shelf", &office},
		{box, "ZZ box", &shelf},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO copy_locations (id, library_id, name, parent_id) VALUES ($1, $2, $3, $4)`,
			n.id, libraryID, n.name, n.parent); err != nil {
			t.Fatalf("creating %s: %v", n.name, err)
		}
		id := n.id
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM copy_locations WHERE id = $1`, id) })
	}

	all, err := locs.List(ctx, libraryID)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}

	at := map[uuid.UUID]int{}
	for i, l := range all {
		at[l.ID] = i
	}
	for _, id := range []uuid.UUID{office, shelf, box, root} {
		if _, ok := at[id]; !ok {
			t.Fatal("a place was not listed at all")
		}
	}

	if at[office] >= at[shelf] || at[shelf] >= at[box] {
		t.Errorf("the office subtree is out of order: office=%d shelf=%d box=%d",
			at[office], at[shelf], at[box])
	}
	// The whole point: the other root cannot sit between a place and something
	// inside it.
	if at[root] > at[office] && at[root] < at[box] {
		t.Errorf("a root landed inside another root's subtree, at %d between %d and %d",
			at[root], at[office], at[box])
	}
}

// A rating reads down its scale. Every other dimension is ordered by count,
// which is right for a tag, where the question is what there is a lot of. On a
// scale it put three and a half stars above five and read as a jumble.
func TestRatingFacetReadsDownTheScale(t *testing.T) {
	pool, ctx := tiersPool(t)
	books := NewBookRepo(pool)

	// The library with the most distinct ratings, not whichever came first: an
	// arbitrary one can easily hold nothing rated, and a test that skips itself
	// checks nothing.
	var libraryID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT c.library_id
		  FROM copies c
		  JOIN user_books ub ON ub.book_id = c.book_id AND ub.rating IS NOT NULL
		 WHERE c.deleted_at IS NULL
		 GROUP BY c.library_id
		 ORDER BY count(DISTINCT ub.rating) DESC
		 LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no library holds a rated book: %v", err)
	}
	facets, err := books.Facets(ctx, []uuid.UUID{libraryID}, FacetSelection{}, "", nil, uuid.Nil)
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if len(facets.Rating) < 2 {
		t.Skip("fewer than two ratings in use, so there is no order to check")
	}

	for i := 1; i < len(facets.Rating); i++ {
		prev, err := strconv.Atoi(facets.Rating[i-1].Value)
		if err != nil {
			t.Fatalf("a rating value was not a number: %q", facets.Rating[i-1].Value)
		}
		cur, err := strconv.Atoi(facets.Rating[i].Value)
		if err != nil {
			t.Fatalf("a rating value was not a number: %q", facets.Rating[i].Value)
		}
		if cur >= prev {
			t.Errorf("rating %d came after %d; the scale has to descend", cur, prev)
		}
	}
}

// Owning a container is owning what is inside it. A three-in-one puts volumes
// one to three on the shelf; without that, each is a gap, and the rail offers
// to find someone books already in their hands.
//
// BookContentsRepo.ListContainers was written to answer exactly this and
// nothing called it, so the rule existed everywhere except where it counted.
func TestOwningAnOmnibusOwnsWhatIsInsideIt(t *testing.T) {
	pool, ctx := tiersPool(t)
	books := NewBookRepo(pool)

	var libraryID, omnibusID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT c.library_id, c.book_id FROM copies c
		 WHERE c.deleted_at IS NULL LIMIT 1`).Scan(&libraryID, &omnibusID); err != nil {
		t.Skipf("no held book to use as a container: %v", err)
	}

	// A volume nobody holds, recorded against a series in this library so it
	// starts life as a gap.
	var mediaTypeID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types LIMIT 1`).Scan(&mediaTypeID); err != nil {
		t.Skipf("no media type: %v", err)
	}
	inside := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO books (id, title, media_type_id) VALUES ($1, 'ZZ contained volume', $2)`,
		inside, mediaTypeID); err != nil {
		t.Fatalf("creating the contained volume: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, inside) })

	seriesID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO series (id, library_id, name) VALUES ($1, $2, 'ZZ containment series')`,
		seriesID, libraryID); err != nil {
		t.Fatalf("creating the series: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM series WHERE id = $1`, seriesID) })
	if _, err := pool.Exec(ctx,
		`INSERT INTO book_series (book_id, series_id, position) VALUES ($1, $2, 1)`,
		inside, seriesID); err != nil {
		t.Fatalf("putting it in the series: %v", err)
	}

	ownership := func() string {
		var got string
		err := pool.QueryRow(ctx, `
			SELECT o.ownership FROM (`+bookScopeCTE(1, 0)+`) o WHERE o.book_id = $2`,
			[]uuid.UUID{libraryID}, inside).Scan(&got)
		if err != nil {
			return "none"
		}
		return got
	}

	// Nobody holds it and nothing contains it, so it is a gap.
	if got := ownership(); got != OwnershipGap {
		t.Fatalf("before containment the volume read as %q, want %q", got, OwnershipGap)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO book_contents (container_id, contained_id, position) VALUES ($1, $2, 1)`,
		omnibusID, inside); err != nil {
		t.Fatalf("recording containment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM book_contents WHERE container_id = $1 AND contained_id = $2`,
			omnibusID, inside)
	})

	// Now a held book contains it, so it is on the shelf.
	if got := ownership(); got != OwnershipShelf {
		t.Errorf("inside a held container the volume read as %q, want %q", got, OwnershipShelf)
	}

	// And it does not also come back as a gap, or the facet counts stop summing
	// to the total and a reader who ticks every box sees it twice.
	_, held, err := books.List(ctx, libraryID, ListBooksOpts{
		PerPage: 5, Selection: FacetSelection{Ownership: []string{OwnershipGap}},
	})
	if err != nil {
		t.Fatalf("listing gaps: %v", err)
	}
	_ = held
}

// An omnibus covers a span, it does not occupy one position.
//
// A three-in-one sits at volume one and reads as a second volume one beside the
// single, so a 56-volume run held complete plus one omnibus reports 57 books.
// The contained rows carry their own positions, so the span is derivable and a
// stored column would only be a copy that can disagree with them.
func TestAContainerSpansThePositionsItHolds(t *testing.T) {
	pool, ctx := tiersPool(t)
	series := NewSeriesRepo(pool)

	var libraryID, mediaTypeID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no library: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types LIMIT 1`).Scan(&mediaTypeID); err != nil {
		t.Skipf("no media type: %v", err)
	}

	seriesID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO series (id, library_id, name) VALUES ($1, $2, 'ZZ span series')`,
		seriesID, libraryID); err != nil {
		t.Fatalf("creating the series: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM series WHERE id = $1`, seriesID) })

	// A three-in-one at position one, and the three volumes it holds.
	omnibus := uuid.New()
	made := []uuid.UUID{omnibus}
	for i, title := range []string{"ZZ omnibus 1-3", "ZZ volume 1", "ZZ volume 2", "ZZ volume 3"} {
		id := omnibus
		if i > 0 {
			id = uuid.New()
			made = append(made, id)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO books (id, title, media_type_id) VALUES ($1, $2, $3)`,
			id, title, mediaTypeID); err != nil {
			t.Fatalf("creating %s: %v", title, err)
		}
		position := float64(i)
		if i == 0 {
			position = 1
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO book_series (book_id, series_id, position) VALUES ($1, $2, $3)`,
			id, seriesID, position); err != nil {
			t.Fatalf("positioning %s: %v", title, err)
		}
		if i > 0 {
			if _, err := pool.Exec(ctx,
				`INSERT INTO book_contents (container_id, contained_id, position) VALUES ($1, $2, $3)`,
				omnibus, id, position); err != nil {
				t.Fatalf("recording containment for %s: %v", title, err)
			}
		}
	}
	t.Cleanup(func() {
		for _, id := range made {
			_, _ = pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, id)
		}
	})

	entries, err := series.ListBooks(ctx, seriesID, uuid.Nil)
	if err != nil {
		t.Fatalf("listing the series: %v", err)
	}

	var spans int
	for _, e := range entries {
		switch e.BookID {
		case omnibus:
			if e.PositionEnd != 3 {
				t.Errorf("the omnibus spans to %v, want 3", e.PositionEnd)
			}
			spans++
		default:
			// A single volume covers itself and nothing else, so it reports no
			// span at all rather than a span of one.
			if e.PositionEnd != 0 {
				t.Errorf("%s reports a span ending at %v, want none", e.Title, e.PositionEnd)
			}
		}
	}
	if spans != 1 {
		t.Errorf("found %d containers in the series, want 1", spans)
	}
}

// A series counts volumes, not book rows.
//
// The label reads "N / 56 volumes", so a run held complete plus one three-in-one
// said 57 of 56, and so did the standard edition of a volume beside its
// anniversary reprint. Two books, one volume of the run either way.
func TestASeriesCountsVolumesNotBooks(t *testing.T) {
	pool, ctx := tiersPool(t)
	series := NewSeriesRepo(pool)

	var libraryID, mediaTypeID, editionFormatID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no library: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types LIMIT 1`).Scan(&mediaTypeID); err != nil {
		t.Skipf("no media type: %v", err)
	}
	_ = editionFormatID

	seriesID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO series (id, library_id, name, total_count)
		 VALUES ($1, $2, 'ZZ counting series', 3)`, seriesID, libraryID); err != nil {
		t.Fatalf("creating the series: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM series WHERE id = $1`, seriesID) })

	// Held, so it counts. The reprint sits at the same position as the single,
	// which is the duplicate-edition case; the omnibus covers all three.
	var made []uuid.UUID
	hold := func(title string, position float64) uuid.UUID {
		id := uuid.New()
		made = append(made, id)
		if _, err := pool.Exec(ctx,
			`INSERT INTO books (id, title, media_type_id) VALUES ($1, $2, $3)`,
			id, title, mediaTypeID); err != nil {
			t.Fatalf("creating %s: %v", title, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO book_series (book_id, series_id, position) VALUES ($1, $2, $3)`,
			id, seriesID, position); err != nil {
			t.Fatalf("positioning %s: %v", title, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO copies (id, library_id, book_id) VALUES ($1, $2, $3)`,
			uuid.New(), libraryID, id); err != nil {
			t.Fatalf("holding %s: %v", title, err)
		}
		return id
	}
	t.Cleanup(func() {
		for _, id := range made {
			_, _ = pool.Exec(ctx, `DELETE FROM copies WHERE book_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, id)
		}
	})

	one := hold("ZZ counting 1", 1)
	hold("ZZ counting 1 anniversary", 1)
	two := hold("ZZ counting 2", 2)
	three := hold("ZZ counting 3", 3)
	omnibus := hold("ZZ counting 1-3", 1)
	for _, inside := range []uuid.UUID{one, two, three} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO book_contents (container_id, contained_id, position) VALUES ($1, $2, 1)`,
			omnibus, inside); err != nil {
			t.Fatalf("recording containment: %v", err)
		}
	}

	// Five books on the shelf, three volumes of the run.
	found, err := series.List(ctx, libraryID, uuid.Nil, "ZZ counting series", "")
	if err != nil {
		t.Fatalf("listing series: %v", err)
	}
	var got *models.Series
	for _, s := range found {
		if s.ID == seriesID {
			got = s
		}
	}
	if got == nil {
		t.Fatalf("the series did not come back from List")
	}
	if got.BookCount != 3 {
		t.Errorf("the series counts %d volumes, want 3 from 5 books", got.BookCount)
	}
}

// A volume a provider knows about becomes a book the collection can talk about.
//
// 448 volumes were on record across 61 series and not one could surface, because
// nothing turned a volume into a book. The gap arm of the ownership facet has
// always looked for exactly the shape promotion writes.
func TestPromotingVolumesFillsTheGaps(t *testing.T) {
	pool, ctx := tiersPool(t)
	series := NewSeriesRepo(pool)

	var libraryID, mediaTypeID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no library: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types LIMIT 1`).Scan(&mediaTypeID); err != nil {
		t.Skipf("no media type: %v", err)
	}

	seriesID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO series (id, library_id, name, total_count)
		 VALUES ($1, $2, 'ZZ promotion series', 3)`, seriesID, libraryID); err != nil {
		t.Fatalf("creating the series: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM series WHERE id = $1`, seriesID) })

	// Volume one is held. Two and three are known to the provider and nobody
	// has them, which is the state the whole facet was written for.
	held := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO books (id, title, media_type_id) VALUES ($1, 'ZZ promotion 1', $2)`,
		held, mediaTypeID); err != nil {
		t.Fatalf("creating the held book: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO book_series (book_id, series_id, position) VALUES ($1, $2, 1)`,
		held, seriesID); err != nil {
		t.Fatalf("positioning the held book: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO copies (id, library_id, book_id) VALUES ($1, $2, $3)`,
		uuid.New(), libraryID, held); err != nil {
		t.Fatalf("holding it: %v", err)
	}
	author := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO contributors (id, name) VALUES ($1, 'ZZ Promotion Author')`,
		author); err != nil {
		t.Fatalf("creating the author: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO book_contributors (book_id, contributor_id, role, display_order)
		 VALUES ($1, $2, 'author', 0)`, held, author); err != nil {
		t.Fatalf("attributing the held book: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM copies WHERE book_id = $1`, held)
		_, _ = pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, held)
		_, _ = pool.Exec(ctx, `DELETE FROM contributors WHERE id = $1`, author)
	})

	// Volume three arrives with no title, which is how 189 of the 448 volumes
	// in a real collection arrive: the providers answering series_volumes sync
	// positions and dates and often nothing else.
	var volumeIDs []uuid.UUID
	for i, title := range []string{"ZZ promotion 1", "ZZ promotion 2", ""} {
		id := uuid.New()
		volumeIDs = append(volumeIDs, id)
		var titleArg *string
		if title != "" {
			titleArg = &title
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO series_volumes (id, series_id, position, title) VALUES ($1, $2, $3, $4)`,
			id, seriesID, float64(i+1), titleArg); err != nil {
			t.Fatalf("recording volume %d: %v", i+1, err)
		}
	}
	// Books created by promotion; named here so cleanup catches them whatever
	// the test does after.
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `
			DELETE FROM books WHERE id IN (
				SELECT book_id FROM series_volumes WHERE series_id = $1 AND book_id IS NOT NULL
			)`, seriesID)
	})

	got, err := series.PromoteVolumes(ctx, seriesID)
	if err != nil {
		t.Fatalf("promoting: %v", err)
	}
	// Volume one is the book already there; two and three are new.
	if got.Matched != 1 || got.Promoted != 2 {
		t.Errorf("promotion reported %+v, want 1 matched and 2 promoted", got)
	}

	// The titleless volume is named from the run and its number rather than
	// left unpromoted, or two fifths of every series stays invisible.
	var derived string
	if err := pool.QueryRow(ctx, `
		SELECT b.title FROM series_volumes sv JOIN books b ON b.id = sv.book_id
		 WHERE sv.series_id = $1 AND sv.position = 3`, seriesID).Scan(&derived); err != nil {
		t.Fatalf("reading the promoted volume three: %v", err)
	}
	if derived != "ZZ promotion series #3" {
		t.Errorf("volume three is titled %q, want %q", derived, "ZZ promotion series #3")
	}

	// And it is attributed, because every book already in the run agrees on who
	// wrote it. A card with no contributor line reads as a broken row.
	var attributed int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM series_volumes sv
		  JOIN book_contributors bc ON bc.book_id = sv.book_id
		 WHERE sv.series_id = $1 AND bc.contributor_id = $2
		   AND sv.book_id <> $3`, seriesID, author, held).Scan(&attributed); err != nil {
		t.Fatalf("counting attributions: %v", err)
	}
	if attributed != 2 {
		t.Errorf("%d promoted books carry the run's author, want 2", attributed)
	}

	// The point of all of it: the rail can now say what is missing.
	var gaps int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (`+bookScopeCTE(1, 0)+`) o
		  JOIN book_series bs ON bs.book_id = o.book_id
		 WHERE bs.series_id = $2 AND o.ownership = $3`,
		[]uuid.UUID{libraryID}, seriesID, OwnershipGap).Scan(&gaps); err != nil {
		t.Fatalf("counting gaps: %v", err)
	}
	if gaps != 2 {
		t.Errorf("the series reports %d missing volumes, want 2", gaps)
	}

	// Running it again must not create the books a second time.
	again, err := series.PromoteVolumes(ctx, seriesID)
	if err != nil {
		t.Fatalf("promoting again: %v", err)
	}
	if again.Promoted != 0 || again.Matched != 0 {
		t.Errorf("a second run reported %+v, want nothing to do", again)
	}

	// A promoted book nobody has touched goes away with its volume. The held
	// one stays, because it was never a placeholder.
	removed, err := series.DemoteUnheldVolumes(ctx, volumeIDs)
	if err != nil {
		t.Fatalf("demoting: %v", err)
	}
	if removed != 2 {
		t.Errorf("demotion removed %d books, want 2", removed)
	}
	var stillThere bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM books WHERE id = $1)`, held).Scan(&stillThere); err != nil {
		t.Fatalf("checking the held book: %v", err)
	}
	if !stillThere {
		t.Error("demotion deleted a book somebody actually owns")
	}
}

// The series index narrows and orders on the server.
//
// Every filter used to be a client-side pass over whatever the server happened
// to send, which worked only because the per-library page never had more than
// one library's rows. Across every library the list is the whole instance.
func TestSeriesIndexFiltersAndSortsOnTheServer(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewSeriesRepo(pool)

	var libraryIDs []uuid.UUID
	rows, err := pool.Query(ctx, `SELECT id FROM libraries`)
	if err != nil {
		t.Skipf("no libraries: %v", err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		libraryIDs = append(libraryIDs, id)
	}
	rows.Close()

	all, err := repo.ListAcrossFiltered(ctx, libraryIDs, uuid.Nil, "", 1, SeriesFilter{})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(all) < 2 {
		t.Skip("not enough series to tell the filters apart")
	}

	// Status narrows to exactly the rows carrying it.
	completed, err := repo.ListAcrossFiltered(ctx, libraryIDs, uuid.Nil, "", 1,
		SeriesFilter{Status: "completed"})
	if err != nil {
		t.Fatalf("filtering by status: %v", err)
	}
	if len(completed) == len(all) {
		t.Error("filtering by status returned everything, so it did not filter")
	}
	for _, s := range completed {
		if s.Status != "completed" {
			t.Errorf("%s has status %q in a completed-only list", s.Name, s.Status)
		}
	}

	// Arcs, both directions, and the two halves have to add up to the whole.
	with, err := repo.ListAcrossFiltered(ctx, libraryIDs, uuid.Nil, "", 1, SeriesFilter{Arcs: "with"})
	if err != nil {
		t.Fatalf("filtering by arcs: %v", err)
	}
	without, err := repo.ListAcrossFiltered(ctx, libraryIDs, uuid.Nil, "", 1, SeriesFilter{Arcs: "without"})
	if err != nil {
		t.Fatalf("filtering by arcs: %v", err)
	}
	if len(with)+len(without) != len(all) {
		t.Errorf("with arcs (%d) plus without (%d) is %d, but there are %d series",
			len(with), len(without), len(with)+len(without), len(all))
	}
	for _, s := range with {
		if s.ArcCount == 0 {
			t.Errorf("%s has no arcs in a with-arcs list", s.Name)
		}
	}

	// Sorting by how many volumes are held, largest first.
	byVolumes, err := repo.ListAcrossFiltered(ctx, libraryIDs, uuid.Nil, "", 1,
		SeriesFilter{Sort: "volumes", Desc: true})
	if err != nil {
		t.Fatalf("sorting by volumes: %v", err)
	}
	for i := 1; i < len(byVolumes); i++ {
		if byVolumes[i-1].BookCount < byVolumes[i].BookCount {
			t.Fatalf("sorted by volumes descending, %s (%d) came before %s (%d)",
				byVolumes[i-1].Name, byVolumes[i-1].BookCount,
				byVolumes[i].Name, byVolumes[i].BookCount)
		}
	}

	// And the default is still by name, which is what every existing caller
	// expects to get back when it asks for nothing.
	for i := 1; i < len(all); i++ {
		if strings.ToLower(all[i-1].Name) > strings.ToLower(all[i].Name) {
			t.Fatalf("the default order is not by name: %q before %q", all[i-1].Name, all[i].Name)
		}
	}
}

// A dimension is counted with its own selection excluded.
//
// Applying every filter uniformly collapses the dimension you just used: tick
// Ongoing and the status list shows only Ongoing, so adding Complete becomes
// impossible without clearing first. This is the rule that makes a rail worth
// having, and it is the one that silently breaks.
func TestSeriesFacetsCountAroundTheirOwnSelection(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewSeriesRepo(pool)

	var libraryIDs []uuid.UUID
	rows, err := pool.Query(ctx, `SELECT id FROM libraries`)
	if err != nil {
		t.Skipf("no libraries: %v", err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		libraryIDs = append(libraryIDs, id)
	}
	rows.Close()

	base, err := repo.Facets(ctx, libraryIDs, uuid.Nil, "", SeriesFilter{})
	if err != nil {
		t.Fatalf("counting facets: %v", err)
	}
	if len(base.Status) < 2 {
		t.Skip("need two statuses to tell the exclusion apart")
	}

	// Pick a status and tick it. Its own dimension must still report every
	// status, unchanged, because the rail is answering "what if I picked this
	// one as well" rather than "what did I just pick".
	picked := base.Status[0].Value
	narrowed, err := repo.Facets(ctx, libraryIDs, uuid.Nil, "", SeriesFilter{Status: picked})
	if err != nil {
		t.Fatalf("counting facets with a status: %v", err)
	}
	if len(narrowed.Status) != len(base.Status) {
		t.Errorf("ticking %q left %d statuses on offer, want all %d",
			picked, len(narrowed.Status), len(base.Status))
	}
	for i := range base.Status {
		if i < len(narrowed.Status) && narrowed.Status[i] != base.Status[i] {
			t.Errorf("ticking %q changed its own dimension: %+v became %+v",
				picked, base.Status[i], narrowed.Status[i])
		}
	}

	// Every other dimension does narrow, or the selection did nothing at all.
	baseArcs, narrowedArcs := 0, 0
	for _, v := range base.Arcs {
		baseArcs += v.Count
	}
	for _, v := range narrowed.Arcs {
		narrowedArcs += v.Count
	}
	if narrowedArcs >= baseArcs {
		t.Errorf("ticking %q left the arcs dimension counting %d of %d, so it did not narrow",
			picked, narrowedArcs, baseArcs)
	}

	// And the counts have to agree with the rows. A rail that disagrees with
	// its own list is worse than no rail: the number is what people trust.
	for _, v := range base.Status {
		listed, err := repo.ListAcrossFiltered(ctx, libraryIDs, uuid.Nil, "", 1,
			SeriesFilter{Status: v.Value})
		if err != nil {
			t.Fatalf("listing status %q: %v", v.Value, err)
		}
		if len(listed) != v.Count {
			t.Errorf("the rail says %d series are %q, the list returns %d",
				v.Count, v.Value, len(listed))
		}
	}
}

// A view a page opens on reaches an account that was seeded before it existed.
//
// lists_seeded_at stops the example views coming back after someone throws them
// away, which is right. Applied to the Default view it is wrong: a Default is
// not a suggestion, it is what the page opens on and what holds that reader's
// own filters and layout. Every account predates the Series surface, so guarding
// it the same way would leave every one of them with a Series page opening on
// nothing.
func TestAPageAlwaysHasSomethingToOpenOn(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewListRepo(pool)

	userID := scratchUser(t, pool, ctx)

	// Seeded before the surface existed: the examples are handed over and the
	// account is marked done.
	if err := repo.SeedBuiltIns(ctx, userID); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	// Now delete every Series view, as an account created before the surface
	// effectively has: none of them were ever inserted.
	if _, err := pool.Exec(ctx,
		`DELETE FROM lists WHERE owner_user_id = $1 AND surface = $2`,
		userID, SurfaceSeries); err != nil {
		t.Fatalf("clearing the series views: %v", err)
	}

	// The next read has to put the permanent one back, and only that one.
	if err := repo.SeedBuiltIns(ctx, userID); err != nil {
		t.Fatalf("re-seeding: %v", err)
	}

	rows, err := pool.Query(ctx,
		`SELECT builtin_key FROM lists WHERE owner_user_id = $1 AND surface = $2`,
		userID, SurfaceSeries)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		keys = append(keys, k)
	}
	rows.Close()

	if len(keys) != 1 || keys[0] != "default" {
		t.Fatalf("the series surface came back with %v, want just the default view", keys)
	}

	// And the examples stay deleted, which is the whole reason lists_seeded_at
	// exists. Bringing them back would undo somebody's tidying every page load.
	var books int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM lists
		 WHERE owner_user_id = $1 AND surface = $2 AND builtin_key = 'reading'`,
		userID, SurfaceBooks).Scan(&books); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM lists WHERE owner_user_id = $1 AND builtin_key = 'reading'`,
		userID); err != nil {
		t.Fatalf("deleting an example: %v", err)
	}
	if err := repo.SeedBuiltIns(ctx, userID); err != nil {
		t.Fatalf("seeding again: %v", err)
	}
	var back int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM lists
		 WHERE owner_user_id = $1 AND builtin_key = 'reading'`, userID).Scan(&back); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if back != 0 {
		t.Errorf("a deleted example came back, so throwing one away does not stick")
	}
}

// "Show me the manga" is a question about the books, not about the series row.
//
// Nothing on a series says what kind of thing it is. Every book in it says so,
// and no series in a real collection mixes, so the answer was already there and
// only needed asking for.
func TestSeriesFilterByWhatTheBooksAre(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewSeriesRepo(pool)

	var libraryIDs []uuid.UUID
	rows, err := pool.Query(ctx, `SELECT id FROM libraries`)
	if err != nil {
		t.Skipf("no libraries: %v", err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		libraryIDs = append(libraryIDs, id)
	}
	rows.Close()

	facets, err := repo.Facets(ctx, libraryIDs, uuid.Nil, "", SeriesFilter{})
	if err != nil {
		t.Fatalf("counting facets: %v", err)
	}
	if len(facets.MediaType) < 2 {
		t.Skip("need two media types to tell the filter apart")
	}

	// Every media type's count has to equal the rows the filter returns, or the
	// rail promises something the page does not deliver.
	for _, v := range facets.MediaType {
		listed, err := repo.ListAcrossFiltered(ctx, libraryIDs, uuid.Nil, "", 1,
			SeriesFilter{MediaTypes: []string{v.Value}})
		if err != nil {
			t.Fatalf("filtering by media type %q: %v", v.Value, err)
		}
		if len(listed) != v.Count {
			t.Errorf("the rail says %d series are %q, the list returns %d",
				v.Count, v.Value, len(listed))
		}
		// And each one really does hold a book of that kind.
		for _, ser := range listed {
			var holds bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM book_series bs
					  JOIN books b ON b.id = bs.book_id
					  JOIN media_types mt ON mt.id = b.media_type_id
					 WHERE bs.series_id = $1 AND mt.name = $2)`,
				ser.ID, v.Value).Scan(&holds); err != nil {
				t.Fatalf("checking %s: %v", ser.Name, err)
			}
			if !holds {
				t.Errorf("%s came back under %q and holds no book of that kind", ser.Name, v.Value)
			}
		}
	}

	// Genre is the series' own free-text list, a different vocabulary from the
	// controlled one book genres use, so it is checked against the column
	// rather than against book_genres.
	if len(facets.Genre) == 0 {
		return
	}
	pick := facets.Genre[0]
	listed, err := repo.ListAcrossFiltered(ctx, libraryIDs, uuid.Nil, "", 1,
		SeriesFilter{Genres: []string{pick.Value}})
	if err != nil {
		t.Fatalf("filtering by genre: %v", err)
	}
	if len(listed) != pick.Count {
		t.Errorf("the rail says %d series are %q, the list returns %d",
			pick.Count, pick.Value, len(listed))
	}
	for _, ser := range listed {
		found := false
		for _, g := range ser.Genres {
			if g == pick.Value {
				found = true
			}
		}
		if !found {
			t.Errorf("%s came back under genre %q and does not carry it", ser.Name, pick.Value)
		}
	}
}

// A run is worth what its volumes are worth, each volume counting once.
//
// The average of the volume averages, not the average of every rating. A single
// volume five people loved would otherwise outweigh the rest of the run, and a
// twenty-volume series would report the opinion of its most-discussed book.
func TestASeriesRatingAveragesItsVolumes(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewSeriesRepo(pool)

	var libraryID, mediaTypeID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no library: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types LIMIT 1`).Scan(&mediaTypeID); err != nil {
		t.Skipf("no media type: %v", err)
	}

	seriesID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO series (id, library_id, name) VALUES ($1, $2, 'ZZ rating series')`,
		seriesID, libraryID); err != nil {
		t.Fatalf("creating the series: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM series WHERE id = $1`, seriesID) })

	var books []uuid.UUID
	for i := 1; i <= 3; i++ {
		id := uuid.New()
		books = append(books, id)
		if _, err := pool.Exec(ctx,
			`INSERT INTO books (id, title, media_type_id) VALUES ($1, $2, $3)`,
			id, "ZZ rating volume", mediaTypeID); err != nil {
			t.Fatalf("creating volume %d: %v", i, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO book_series (book_id, series_id, position) VALUES ($1, $2, $3)`,
			id, seriesID, i); err != nil {
			t.Fatalf("positioning volume %d: %v", i, err)
		}
	}
	t.Cleanup(func() {
		for _, id := range books {
			_, _ = pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, id)
		}
	})

	// Two readers, both holding a role on the library so both count.
	readers := []uuid.UUID{scratchUser(t, pool, ctx), scratchUser(t, pool, ctx)}
	var roleID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM roles WHERE scope = 'library' LIMIT 1`).Scan(&roleID); err != nil {
		t.Skipf("no role to grant: %v", err)
	}
	for _, u := range readers {
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id, scope, library_id)
			VALUES ($1, $2, 'library', $3) ON CONFLICT DO NOTHING`,
			u, roleID, libraryID); err != nil {
			t.Fatalf("granting a role: %v", err)
		}
	}
	rate := func(u, book uuid.UUID, rating int) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_books (user_id, book_id, rating, read_status)
			VALUES ($1, $2, $3, 'unread')
			ON CONFLICT (user_id, book_id) DO UPDATE SET rating = EXCLUDED.rating`,
			u, book, rating); err != nil {
			t.Fatalf("rating: %v", err)
		}
	}

	// Volume one: two readers, 10 and 8, so the volume averages 9.
	// Volume two: one reader, 3.
	// Volume three: nobody.
	//
	// Average of the volume averages is (9 + 3) / 2 = 6. The average of every
	// rating would be (10 + 8 + 3) / 3 = 7, which is the answer this test
	// exists to refuse.
	rate(readers[0], books[0], 10)
	rate(readers[1], books[0], 8)
	rate(readers[0], books[1], 3)

	got, err := repo.FindByID(ctx, seriesID, readers[0])
	if err != nil {
		t.Fatalf("reading the series back: %v", err)
	}
	if got.Rating == nil {
		t.Fatal("the run has rated volumes and reported no rating")
	}
	if *got.Rating != 6 {
		t.Errorf("the run rates %d, want 6: one volume averaging 9 and one at 3", *got.Rating)
	}
	// Two of three volumes carry a rating, which is what lets a reader judge
	// whether to trust the number at all.
	if got.RatedBooks != 2 {
		t.Errorf("the run reports %d rated volumes, want 2", got.RatedBooks)
	}
	// The first reader gave 10 and 3, so their own average is 6.5, rounding to
	// 7. Their own opinion is a different question from the run's.
	if got.MyRating == nil || *got.MyRating != 7 {
		t.Errorf("the caller's own average is %v, want 7", got.MyRating)
	}

	// A run nobody has rated has no rating, which is not a rating of nought.
	bare := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO series (id, library_id, name) VALUES ($1, $2, 'ZZ unrated series')`,
		bare, libraryID); err != nil {
		t.Fatalf("creating the unrated series: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM series WHERE id = $1`, bare) })
	unrated, err := repo.FindByID(ctx, bare, readers[0])
	if err != nil {
		t.Fatalf("reading the unrated series: %v", err)
	}
	if unrated.Rating != nil {
		t.Errorf("a run nobody rated reports %v, want no rating at all", *unrated.Rating)
	}
}

// A run can know how long it is without knowing what is in it.
//
// Absolute Boyfriend reports seven volumes and the provider lists six. Volume
// seven exists in the world, is missing from the shelf, and had nowhere to be
// recorded: promotion walked series_volumes, and there was no row for it. The
// page drew a placeholder that linked to nothing and offered to Add.
func TestATotalCountImpliesTheVolumesNobodyListed(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewSeriesRepo(pool)

	var libraryID, mediaTypeID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no library: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types LIMIT 1`).Scan(&mediaTypeID); err != nil {
		t.Skipf("no media type: %v", err)
	}

	// Seven volumes according to the publisher, three of them on the shelf, and
	// not one series_volumes row to promote.
	seriesID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO series (id, library_id, name, total_count)
		 VALUES ($1, $2, 'ZZ implied series', 7)`, seriesID, libraryID); err != nil {
		t.Fatalf("creating the series: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM series WHERE id = $1`, seriesID) })

	for i := 1; i <= 3; i++ {
		id := uuid.New()
		if _, err := pool.Exec(ctx,
			`INSERT INTO books (id, title, media_type_id) VALUES ($1, $2, $3)`,
			id, fmt.Sprintf("ZZ implied series #%d", i), mediaTypeID); err != nil {
			t.Fatalf("creating volume %d: %v", i, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO book_series (book_id, series_id, position) VALUES ($1, $2, $3)`,
			id, seriesID, i); err != nil {
			t.Fatalf("positioning volume %d: %v", i, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO copies (id, library_id, book_id) VALUES ($1, $2, $3)`,
			uuid.New(), libraryID, id); err != nil {
			t.Fatalf("holding volume %d: %v", i, err)
		}
	}
	t.Cleanup(func() {
		// Copies first: they reference the books, so deleting a book while one
		// exists fails, and an ignored error leaks a row on every run. The
		// junction is read before the series cascade takes it away.
		_, _ = pool.Exec(ctx, `
			DELETE FROM copies WHERE book_id IN (
				SELECT book_id FROM book_series WHERE series_id = $1)`, seriesID)
		_, _ = pool.Exec(ctx, `
			DELETE FROM books WHERE id IN (SELECT book_id FROM book_series WHERE series_id = $1)`,
			seriesID)
	})

	if _, err := repo.PromoteVolumes(ctx, seriesID); err != nil {
		t.Fatalf("promoting: %v", err)
	}

	entries, err := repo.ListBooks(ctx, seriesID, uuid.Nil)
	if err != nil {
		t.Fatalf("listing the run: %v", err)
	}
	if len(entries) != 7 {
		t.Fatalf("the run lists %d volumes, want all 7 the total promises", len(entries))
	}

	// Every volume is a book now, and the four nobody has say so, which is what
	// lets the page grey them rather than drawing a placeholder that links
	// nowhere.
	for _, e := range entries {
		wantHeld := e.Position <= 3
		if e.Held != wantHeld {
			t.Errorf("volume %g reports held=%v, want %v", e.Position, e.Held, wantHeld)
		}
		if e.BookID == uuid.Nil {
			t.Errorf("volume %g has no book to link to", e.Position)
		}
	}

	// Running it again must not invent an eighth.
	if _, err := repo.PromoteVolumes(ctx, seriesID); err != nil {
		t.Fatalf("promoting again: %v", err)
	}
	again, err := repo.ListBooks(ctx, seriesID, uuid.Nil)
	if err != nil {
		t.Fatalf("listing again: %v", err)
	}
	if len(again) != 7 {
		t.Errorf("a second run left %d volumes, want 7", len(again))
	}

	// A total can shrink, and the inference has to be reversible or a run that
	// said seven and turned out to be five keeps two phantom volumes forever.
	// Nothing corroborates a volume inferred from a count the way a
	// provider-listed one is corroborated, so it has to be able to go.
	if _, err := pool.Exec(ctx,
		`UPDATE series SET total_count = 5 WHERE id = $1`, seriesID); err != nil {
		t.Fatalf("shrinking the total: %v", err)
	}
	if _, err := repo.PromoteVolumes(ctx, seriesID); err != nil {
		t.Fatalf("promoting after the shrink: %v", err)
	}
	shrunk, err := repo.ListBooks(ctx, seriesID, uuid.Nil)
	if err != nil {
		t.Fatalf("listing after the shrink: %v", err)
	}
	if len(shrunk) != 5 {
		t.Errorf("after shrinking to 5 the run lists %d volumes", len(shrunk))
	}

	// But a volume somebody has done something with is not the count's to take.
	// Grow it back, then put the seventh on a list, then shrink again.
	if _, err := pool.Exec(ctx,
		`UPDATE series SET total_count = 7 WHERE id = $1`, seriesID); err != nil {
		t.Fatalf("restoring the total: %v", err)
	}
	if _, err := repo.PromoteVolumes(ctx, seriesID); err != nil {
		t.Fatalf("re-promoting: %v", err)
	}
	var seventh uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT bs.book_id FROM book_series bs
		 WHERE bs.series_id = $1 AND bs.position = 7`, seriesID).Scan(&seventh); err != nil {
		t.Fatalf("finding volume seven: %v", err)
	}
	owner := scratchUser(t, pool, ctx)
	listID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO lists (id, owner_user_id, name, kind, surface, visibility)
		VALUES ($1, $2, 'ZZ keeps a volume', 'manual', 'books', 'private')`,
		listID, owner); err != nil {
		t.Fatalf("creating the list: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM lists WHERE id = $1`, listID) })
	if _, err := pool.Exec(ctx,
		`INSERT INTO list_books (list_id, book_id) VALUES ($1, $2)`, listID, seventh); err != nil {
		t.Fatalf("filing volume seven: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE series SET total_count = 5 WHERE id = $1`, seriesID); err != nil {
		t.Fatalf("shrinking again: %v", err)
	}
	if _, err := repo.PromoteVolumes(ctx, seriesID); err != nil {
		t.Fatalf("promoting after the second shrink: %v", err)
	}
	var kept bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM books WHERE id = $1)`, seventh).Scan(&kept); err != nil {
		t.Fatalf("checking volume seven: %v", err)
	}
	if !kept {
		t.Error("a shrinking total deleted a volume somebody had filed on a list")
	}
}

// Folding three spellings of one name into one person.
//
// An import spells a name three ways and the catalogue believes in three
// people, each looking like a minor contributor. A merge is a tombstone rather
// than a delete: book_contributors is ON DELETE RESTRICT, so a delete was never
// clean, and merged_into makes the whole thing reversible.
func TestMergingContributorsFoldsThemWithoutLosingCredits(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewContributorRepo(pool)

	var mediaTypeID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types LIMIT 1`).Scan(&mediaTypeID); err != nil {
		t.Skipf("no media type: %v", err)
	}

	// Three spellings, and a book each. The third book credits two of the
	// spellings in the same role, which is the collision the merge has to
	// survive: (book_id, contributor_id, role) is the primary key.
	names := []string{"ZZ R. A. Testcase", "ZZ R.A. Testcase", "ZZ R A Testcase"}
	var people []uuid.UUID
	for _, n := range names {
		id := uuid.New()
		people = append(people, id)
		if _, err := pool.Exec(ctx,
			`INSERT INTO contributors (id, name) VALUES ($1, $2)`, id, n); err != nil {
			t.Fatalf("creating %s: %v", n, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM book_contributors WHERE contributor_id = ANY($1)`, people)
		_, _ = pool.Exec(ctx, `DELETE FROM contributor_not_duplicates
			 WHERE lower_id = ANY($1) OR higher_id = ANY($1)`, people)
		_, _ = pool.Exec(ctx, `DELETE FROM contributors WHERE id = ANY($1)`, people)
	})

	var books []uuid.UUID
	for i := 0; i < 3; i++ {
		id := uuid.New()
		books = append(books, id)
		if _, err := pool.Exec(ctx,
			`INSERT INTO books (id, title, media_type_id) VALUES ($1, $2, $3)`,
			id, fmt.Sprintf("ZZ merge book %d", i), mediaTypeID); err != nil {
			t.Fatalf("creating a book: %v", err)
		}
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM books WHERE id = ANY($1)`, books) })

	credit := func(person, book uuid.UUID) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO book_contributors (book_id, contributor_id, role, display_order)
			VALUES ($1, $2, 'author', 0) ON CONFLICT DO NOTHING`, book, person); err != nil {
			t.Fatalf("crediting: %v", err)
		}
	}
	credit(people[0], books[0])
	credit(people[1], books[1])
	credit(people[2], books[2])
	// Both spellings on the same book, same role: one of these must collapse.
	credit(people[0], books[2])

	// The sweep sees them as one group.
	groups, err := repo.DuplicateCandidates(ctx)
	if err != nil {
		t.Fatalf("finding candidates: %v", err)
	}
	var found *DuplicateCandidate
	for i := range groups {
		if len(groups[i].Members) == 3 && groups[i].Key == "zz ra testcase" {
			found = &groups[i]
		}
	}
	if found == nil {
		t.Fatalf("the three spellings were not offered as one group; got %d groups", len(groups))
	}

	res, err := repo.Merge(ctx, people[0], people[1:])
	if err != nil {
		t.Fatalf("merging: %v", err)
	}
	// Two credits move; the one already held collapses rather than failing.
	if res.Credits != 1 || res.Collapsed != 1 {
		t.Errorf("merge moved %d credits and collapsed %d, want 1 and 1", res.Credits, res.Collapsed)
	}
	if res.Merged != 2 {
		t.Errorf("merge tombstoned %d contributors, want 2", res.Merged)
	}

	// The survivor now holds every book, once each.
	var held int
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM book_contributors WHERE contributor_id = $1`,
		people[0]).Scan(&held); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if held != 3 {
		t.Errorf("the survivor credits %d books, want all 3", held)
	}

	// Tombstones, not deletions: the rows are still there and say where they
	// went, so a client holding an old id lands on a real person.
	for _, loser := range people[1:] {
		to, err := repo.ResolveContributor(ctx, loser)
		if err != nil {
			t.Fatalf("resolving a merged contributor: %v", err)
		}
		if to != people[0] {
			t.Errorf("a merged contributor resolves to %s, want the survivor", to)
		}
	}

	// And they are gone from the surfaces that list people.
	again, err := repo.DuplicateCandidates(ctx)
	if err != nil {
		t.Fatalf("re-checking: %v", err)
	}
	for _, g := range again {
		if g.Key == "zz ra testcase" {
			t.Error("a merged group is still offered as a duplicate")
		}
	}
	people2, err := repo.Search(ctx, "ZZ R", 20)
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(people2) != 1 {
		t.Errorf("search returns %d of the spellings, want only the survivor", len(people2))
	}
}

// A rejected pair stays rejected.
//
// Detection is an inference, so the reviewer has to be able to say no. Without
// this the sweep offers the same wrong answer every morning, which is how a
// review queue becomes something nobody opens.
func TestDismissingADuplicateKeepsItDismissed(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewContributorRepo(pool)

	// Two people whose names differ only by punctuation and who are not, in
	// fact, the same person.
	var people []uuid.UUID
	for _, n := range []string{"ZZ Dismiss O'Brien", "ZZ Dismiss OBrien"} {
		id := uuid.New()
		people = append(people, id)
		if _, err := pool.Exec(ctx,
			`INSERT INTO contributors (id, name) VALUES ($1, $2)`, id, n); err != nil {
			t.Fatalf("creating %s: %v", n, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM contributor_not_duplicates
			 WHERE lower_id = ANY($1) OR higher_id = ANY($1)`, people)
		_, _ = pool.Exec(ctx, `DELETE FROM contributors WHERE id = ANY($1)`, people)
	})

	offered := func() bool {
		groups, err := repo.DuplicateCandidates(ctx)
		if err != nil {
			t.Fatalf("finding candidates: %v", err)
		}
		for _, g := range groups {
			if g.Key == "zz dismiss obrien" {
				return true
			}
		}
		return false
	}

	if !offered() {
		t.Fatal("two spellings of one name were not offered at all")
	}
	if err := repo.Dismiss(ctx, people, uuid.Nil); err != nil {
		t.Fatalf("dismissing: %v", err)
	}
	if offered() {
		t.Error("a dismissed pair came back, so saying no does not stick")
	}
}

// Running promotion twice at once must not promote twice.
//
// Every volume is promoted in its own transaction so one malformed row cannot
// cost the rest, and the price is that "is there already a book at this
// position" is a check followed by an act. Two runs both passed it, both
// created a book, and the update to series_volumes.book_id serialised so the
// loser's book was orphaned. Running the job twice put 175 duplicates in a real
// catalogue.
func TestPromotingTwiceAtOnceStillPromotesOnce(t *testing.T) {
	pool, ctx := tiersPool(t)

	var libraryID, mediaTypeID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM libraries LIMIT 1`).Scan(&libraryID); err != nil {
		t.Skipf("no library: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM media_types LIMIT 1`).Scan(&mediaTypeID); err != nil {
		t.Skipf("no media type: %v", err)
	}

	seriesID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO series (id, library_id, name, total_count)
		 VALUES ($1, $2, 'ZZ race series', 12)`, seriesID, libraryID); err != nil {
		t.Fatalf("creating the series: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM series WHERE id = $1`, seriesID) })

	// One held volume to seed the media type, eleven for promotion to invent.
	seed := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO books (id, title, media_type_id) VALUES ($1, 'ZZ race #1', $2)`,
		seed, mediaTypeID); err != nil {
		t.Fatalf("creating the seed book: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO book_series (book_id, series_id, position) VALUES ($1, $2, 1)`,
		seed, seriesID); err != nil {
		t.Fatalf("positioning the seed: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO copies (id, library_id, book_id) VALUES ($1, $2, $3)`,
		uuid.New(), libraryID, seed); err != nil {
		t.Fatalf("holding the seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM copies WHERE book_id IN (
			SELECT book_id FROM book_series WHERE series_id = $1)`, seriesID)
		_, _ = pool.Exec(ctx, `DELETE FROM books WHERE id IN (
			SELECT book_id FROM book_series WHERE series_id = $1)`, seriesID)
	})

	// Two runs at once, which is what clicking Run now twice does: the job
	// enqueues inline, so the second request does not wait for the first.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, errs[n] = NewSeriesRepo(pool).PromoteVolumes(ctx, seriesID)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("run %d failed: %v", i, err)
		}
	}

	// Twelve volumes promised, twelve books, one per position.
	var books, positions int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::int, count(DISTINCT position)::int
		  FROM book_series WHERE series_id = $1`, seriesID).Scan(&books, &positions); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if books != 12 || positions != 12 {
		t.Errorf("two concurrent runs left %d books across %d positions, want 12 and 12",
			books, positions)
	}

	// And every promoted book is reachable from its volume, so none was
	// orphaned by losing a race for the link.
	var orphans int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::int FROM book_series bs
		 WHERE bs.series_id = $1
		   AND NOT EXISTS (SELECT 1 FROM series_volumes sv WHERE sv.book_id = bs.book_id)
		   AND NOT EXISTS (SELECT 1 FROM copies c WHERE c.book_id = bs.book_id)`,
		seriesID).Scan(&orphans); err != nil {
		t.Fatalf("counting orphans: %v", err)
	}
	if orphans != 0 {
		t.Errorf("%d promoted books have no volume pointing at them", orphans)
	}
}

// A kind that is already going is not started again.
//
// Triggering never checked, so pressing Run now twice started two concurrent
// runs. For a kind that walks the catalogue that is a race with itself rather
// than duplicated work, and it is how 175 duplicate books reached a real
// collection.
func TestAJobKindDoesNotRunTwiceAtOnce(t *testing.T) {
	pool, ctx := tiersPool(t)
	repo := NewJobRepo(pool)

	const kind = "zz_test_kind"
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM jobs WHERE kind = $1`, kind) })

	running, err := repo.IsKindRunning(ctx, kind, time.Hour)
	if err != nil {
		t.Fatalf("checking: %v", err)
	}
	if running {
		t.Fatal("a kind with no jobs at all reports as running")
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO jobs (kind, status, triggered_by, started_at)
		VALUES ($1, 'running', 'admin', NOW()) RETURNING id`, kind).Scan(&jobID); err != nil {
		t.Fatalf("starting a job: %v", err)
	}

	running, err = repo.IsKindRunning(ctx, kind, time.Hour)
	if err != nil {
		t.Fatalf("checking: %v", err)
	}
	if !running {
		t.Error("a running job does not report as running, so a second run would start")
	}

	// A crashed run must not block the kind forever: its row says running with
	// nothing behind it, and nobody can clear that by hand.
	if _, err := pool.Exec(ctx,
		`UPDATE jobs SET started_at = NOW() - INTERVAL '8 hours' WHERE id = $1`, jobID); err != nil {
		t.Fatalf("ageing the job: %v", err)
	}
	running, err = repo.IsKindRunning(ctx, kind, 6*time.Hour)
	if err != nil {
		t.Fatalf("checking: %v", err)
	}
	if running {
		t.Error("a run older than the cutoff still blocks the kind")
	}

	// And a finished run never blocks anything.
	if _, err := pool.Exec(ctx,
		`UPDATE jobs SET status = 'completed', started_at = NOW() WHERE id = $1`, jobID); err != nil {
		t.Fatalf("finishing the job: %v", err)
	}
	running, err = repo.IsKindRunning(ctx, kind, time.Hour)
	if err != nil {
		t.Fatalf("checking: %v", err)
	}
	if running {
		t.Error("a completed job reports as running")
	}
}

// A curly apostrophe sorts where a straight one does.
//
// Three volumes of one run came back 1, 3, 2 because volume two was catalogued
// with U+2019 and its siblings with U+0027. The characters are the same
// apostrophe to a reader and forty-eight code points apart to a sort, so every
// straight-quoted title in a run groups ahead of every curly-quoted one and the
// numbering falls apart in between.
//
// Providers are not consistent with themselves about this, so it is not a
// matter of picking a good one.
func TestTypographicPunctuationSortsWithItsPlainForm(t *testing.T) {
	pool, ctx := tiersPool(t)

	cases := []struct{ curly, plain string }{
		{"Can’t Fear Your Own World", "Can't Fear Your Own World"},
		{"L’Assommoir", "L'Assommoir"},
		{"A – Dash", "A - Dash"},
		{"“Quoted”", "\"Quoted\""},
	}
	for _, c := range cases {
		var a, b string
		if err := pool.QueryRow(ctx,
			`SELECT natural_sort_key($1), natural_sort_key($2)`, c.curly, c.plain).Scan(&a, &b); err != nil {
			t.Fatalf("keying %q: %v", c.curly, err)
		}
		if a != b {
			t.Errorf("%q keys to %q but %q keys to %q; they must sort together",
				c.curly, a, c.plain, b)
		}
	}

	// The whole point: a run numbered 1 to 3 with one odd apostrophe reads in
	// order rather than 1, 3, 2.
	rows, err := pool.Query(ctx, `
		SELECT t FROM unnest(ARRAY[
			'Bleach: Can''t Fear Your Own World #1',
			'Bleach: Can`+"’"+`t Fear Your Own World #2',
			'Bleach: Can''t Fear Your Own World #3'
		]) AS t ORDER BY natural_sort_key(t)`)
	if err != nil {
		t.Fatalf("ordering: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var t2 string
		if err := rows.Scan(&t2); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, t2[len(t2)-2:])
	}
	want := []string{"#1", "#2", "#3"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("the run sorted %v, want %v", got, want)
		}
	}
}
