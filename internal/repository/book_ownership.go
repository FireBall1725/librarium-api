// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import "fmt"

// Ownership: where a book stands in relation to the caller.
//
// Until now the book surface meant "books in libraries you can read", which is
// a join. That cannot express a book you do not own, and every interesting
// ownership state is exactly that: a book you were suggested, one you put on a
// wishlist, or a volume missing from a series you hold part of.
//
// So the scope becomes a union rather than a join. Each arm contributes book
// ids from a different source, and each source carries its own permission:
// library membership for the shelf, the caller's own rows for wishlist and
// suggestions, library membership again for the series a gap belongs to. A
// book nobody can justify seeing appears in no arm and is therefore invisible,
// which is the same guarantee the join gave.

// OwnershipShelf and friends are the values the client filters on.
const (
	OwnershipShelf     = "shelf"
	OwnershipWishlist  = "wishlist"
	OwnershipSuggested = "suggested"
	OwnershipGap       = "gap"
)

// OwnershipValues is the display order of the facet, which is also the order
// the reader reads: what you have, then what you want, then what was proposed,
// then what is missing.
var OwnershipValues = []string{
	OwnershipShelf, OwnershipWishlist, OwnershipSuggested, OwnershipGap,
}

// ownershipRank decides which state wins when a book qualifies for several.
//
// The states have to be mutually exclusive or the facet counts stop summing to
// the total, and a reader who ticks every box would see some books twice. The
// order is not arbitrary: owning a book beats every hypothetical about it, and
// putting something on a wishlist is the reader's own act, so it outranks a
// machine's suggestion. A gap is what is left when nothing else applies.
const ownershipRank = `CASE o.ownership
        WHEN '` + OwnershipShelf + `' THEN 1
        WHEN '` + OwnershipWishlist + `' THEN 2
        WHEN '` + OwnershipSuggested + `' THEN 3
        ELSE 4 END`

// bookScopeCTE builds the scope: one row per book, carrying the single
// ownership state that won.
//
// libraryArg holds the caller's readable library ids; callerArg holds the user
// id, or 0 when there is no caller, in which case the two personal arms are
// omitted rather than bound to nothing.
func bookScopeCTE(libraryArg, callerArg int) string {
	personal := ""
	if callerArg > 0 {
		personal = fmt.Sprintf(`
        UNION ALL
        SELECT w.book_id, '%s' FROM wishlist_items w
        WHERE w.user_id = $%d AND w.book_id IS NOT NULL
        UNION ALL
        SELECT s.book_id, '%s' FROM ai_suggestions s
        WHERE s.user_id = $%d AND s.status = 'new'`,
			OwnershipWishlist, callerArg, OwnershipSuggested, callerArg)
	}

	return fmt.Sprintf(`
    WITH RECURSIVE contained AS (
            SELECT bc.contained_id, 1 AS depth
              FROM book_contents bc
              JOIN held_books h ON h.book_id = bc.container_id
             WHERE h.library_id = ANY($%d) AND h.deleted_at IS NULL
        UNION
            SELECT bc.contained_id, c.depth + 1
              FROM contained c
              JOIN book_contents bc ON bc.container_id = c.contained_id
             WHERE c.depth < 32
    )
    SELECT DISTINCT ON (o.book_id) o.book_id, o.ownership
    FROM (
        SELECT lb.book_id, '%s' AS ownership FROM held_books lb
        WHERE lb.library_id = ANY($%d) AND lb.deleted_at IS NULL
        UNION ALL
        -- Owning a container is owning what is inside it. A three-in-one puts
        -- volumes one to three on the shelf, and without this arm each of them
        -- is a gap: the rail offers to find someone books already in their
        -- hands, and the series reports holes in a run that is complete.
        --
        -- Downward from what is held, bounded, because a cycle in the data
        -- would otherwise hang every read of the collection.
        SELECT c.contained_id, '%s' FROM contained c%s
        UNION ALL
        -- A gap is a book already recorded against a series in one of your
        -- libraries that no library holds. Series membership is what makes it
        -- yours to see; without that arm a volume you have never heard of
        -- would surface just because someone else lacks it too.
        SELECT bs.book_id, '%s' FROM book_series bs
        JOIN series se ON se.id = bs.series_id
        WHERE se.library_id = ANY($%d)
    ) o
    ORDER BY o.book_id, %s`,
		libraryArg,
		OwnershipShelf, libraryArg, OwnershipShelf, personal,
		OwnershipGap, libraryArg, ownershipRank)
}
