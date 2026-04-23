-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- Book ↔ Library M2M refactor: dedupe phase.
--
-- Before M2M, the same ISBN imported into two libraries created two distinct
-- `books` rows + two distinct `book_editions` rows. This migration collapses
-- ISBN-duplicates to single canonical rows and repoints every FK. The
-- canonical row for any group is always the earliest-created one.
--
-- Order of operations matters:
--   1. Dedupe BOOKS first — for each ISBN shared across multiple books via
--      their editions, merge the losers into the earliest-created book.
--      After this, all editions that share an ISBN belong to the same book.
--   2. Dedupe EDITIONS second — within a single book, collapse editions
--      that share ISBN-13 or ISBN-10 into the earliest-created edition.
--
-- Only strictly well-formed ISBNs participate in the dedupe:
--   ISBN-13: exactly 13 digits
--   ISBN-10: exactly 10 characters, trailing check-digit may be 0-9 or X
-- This guards against corrupt/truncated ISBN fields mis-merging distinct
-- books — seen in the wild where ISBN-10 got stored as the first 11
-- characters of an ISBN-13 (two different books in a series shared a
-- publisher prefix and looked identical under a permissive filter).
--
-- Books with no valid ISBN on any edition are not deduped — title/author
-- matching is too lossy to do automatically. They remain as separate rows;
-- a hand-curated pass can merge them later if needed.

-- ── Helper: merge a loser book into a canonical book ───────────────────────

CREATE OR REPLACE FUNCTION _m2m_merge_book(canonical_id UUID, loser_id UUID)
RETURNS VOID AS $$
BEGIN
    -- book_series: PK (book_id, series_id).
    DELETE FROM book_series
     WHERE book_id = loser_id
       AND series_id IN (SELECT series_id FROM book_series WHERE book_id = canonical_id);
    UPDATE book_series SET book_id = canonical_id WHERE book_id = loser_id;

    -- book_contributors: PK (book_id, contributor_id, role).
    DELETE FROM book_contributors
     WHERE book_id = loser_id
       AND (contributor_id, role) IN (
             SELECT contributor_id, role FROM book_contributors WHERE book_id = canonical_id);
    UPDATE book_contributors SET book_id = canonical_id WHERE book_id = loser_id;

    -- book_genres: PK (book_id, genre_id).
    DELETE FROM book_genres
     WHERE book_id = loser_id
       AND genre_id IN (SELECT genre_id FROM book_genres WHERE book_id = canonical_id);
    UPDATE book_genres SET book_id = canonical_id WHERE book_id = loser_id;

    -- book_shelves: PK (book_id, shelf_id).
    DELETE FROM book_shelves
     WHERE book_id = loser_id
       AND shelf_id IN (SELECT shelf_id FROM book_shelves WHERE book_id = canonical_id);
    UPDATE book_shelves SET book_id = canonical_id WHERE book_id = loser_id;

    -- book_tags: PK (book_id, tag_id).
    DELETE FROM book_tags
     WHERE book_id = loser_id
       AND tag_id IN (SELECT tag_id FROM book_tags WHERE book_id = canonical_id);
    UPDATE book_tags SET book_id = canonical_id WHERE book_id = loser_id;

    -- book_editions: move over; conflicts (same ISBN) are handled in phase 2.
    UPDATE book_editions SET book_id = canonical_id WHERE book_id = loser_id;

    -- library_books: UNIQUE(library_id, book_id).
    DELETE FROM library_books
     WHERE book_id = loser_id
       AND library_id IN (SELECT library_id FROM library_books WHERE book_id = canonical_id);
    UPDATE library_books SET book_id = canonical_id WHERE book_id = loser_id;

    -- loans: no uniqueness on book_id.
    UPDATE loans SET book_id = canonical_id WHERE book_id = loser_id;

    -- Nullable FK columns — simple repoint, no conflicts.
    UPDATE wishlist_items         SET book_id = canonical_id WHERE book_id = loser_id;
    UPDATE import_job_items       SET book_id = canonical_id WHERE book_id = loser_id;
    UPDATE enrichment_batch_items SET book_id = canonical_id WHERE book_id = loser_id;
    UPDATE ai_suggestions         SET book_id = canonical_id WHERE book_id = loser_id;

    -- enrichment_batches.book_ids is a JSONB UUID array — swap the loser.
    UPDATE enrichment_batches
       SET book_ids = (
             SELECT to_jsonb(array_agg(DISTINCT v))
               FROM jsonb_array_elements_text(book_ids) AS t(raw),
                    LATERAL (SELECT CASE WHEN raw = loser_id::text
                                         THEN canonical_id::text
                                         ELSE raw END AS v) s
           )
     WHERE book_ids @> to_jsonb(loser_id::text);

    DELETE FROM books WHERE id = loser_id;
END;
$$ LANGUAGE plpgsql;

-- ── Helper: merge a loser edition into a canonical edition ─────────────────

CREATE OR REPLACE FUNCTION _m2m_merge_edition(canonical_id UUID, loser_id UUID)
RETURNS VOID AS $$
BEGIN
    -- user_book_interactions: UNIQUE(user_id, book_edition_id). If both
    -- exist for a user, keep the more-recently-updated side's fields on
    -- the canonical row, then drop the loser.
    UPDATE user_book_interactions c
       SET read_status   = CASE WHEN l.updated_at > c.updated_at THEN l.read_status   ELSE c.read_status   END,
           rating        = COALESCE(CASE WHEN l.updated_at > c.updated_at THEN l.rating        ELSE c.rating        END, c.rating),
           notes         = COALESCE(CASE WHEN l.updated_at > c.updated_at THEN l.notes         ELSE c.notes         END, c.notes),
           review        = COALESCE(CASE WHEN l.updated_at > c.updated_at THEN l.review        ELSE c.review        END, c.review),
           date_started  = COALESCE(l.date_started,  c.date_started),
           date_finished = COALESCE(l.date_finished, c.date_finished),
           progress      = COALESCE(CASE WHEN l.updated_at > c.updated_at THEN l.progress      ELSE c.progress      END, c.progress),
           is_favorite   = l.is_favorite OR c.is_favorite,
           reread_count  = GREATEST(l.reread_count, c.reread_count),
           updated_at    = GREATEST(l.updated_at, c.updated_at)
      FROM user_book_interactions l
     WHERE l.book_edition_id = loser_id
       AND c.book_edition_id = canonical_id
       AND l.user_id         = c.user_id;

    DELETE FROM user_book_interactions
     WHERE book_edition_id = loser_id
       AND user_id IN (SELECT user_id FROM user_book_interactions
                        WHERE book_edition_id = canonical_id);

    UPDATE user_book_interactions
       SET book_edition_id = canonical_id
     WHERE book_edition_id = loser_id;

    -- edition_files: no uniqueness, straight repoint.
    UPDATE edition_files
       SET edition_id = canonical_id
     WHERE edition_id = loser_id;

    -- ai_suggestions.book_edition_id: nullable, no uniqueness, repoint.
    UPDATE ai_suggestions
       SET book_edition_id = canonical_id
     WHERE book_edition_id = loser_id;

    -- library_book_editions: PK (library_id, book_edition_id). Sum copies
    -- and take the earliest acquired_at when a library held both.
    UPDATE library_book_editions c
       SET copy_count  = c.copy_count + l.copy_count,
           acquired_at = LEAST(c.acquired_at, l.acquired_at)
      FROM library_book_editions l
     WHERE l.book_edition_id = loser_id
       AND c.book_edition_id = canonical_id
       AND l.library_id      = c.library_id;

    DELETE FROM library_book_editions
     WHERE book_edition_id = loser_id
       AND library_id IN (SELECT library_id FROM library_book_editions
                           WHERE book_edition_id = canonical_id);

    UPDATE library_book_editions
       SET book_edition_id = canonical_id
     WHERE book_edition_id = loser_id;

    DELETE FROM book_editions WHERE id = loser_id;
END;
$$ LANGUAGE plpgsql;

-- ── Phase 1: dedupe books that share an ISBN via any edition ───────────────
--
-- Find every ISBN (13 first, then 10) where editions owned by multiple
-- books match. Those books are duplicates of the same work — merge them
-- into the earliest-created row.

DO $$
DECLARE
    isbn TEXT;
    canonical_id UUID;
    loser RECORD;
BEGIN
    -- ISBN-13 pass
    FOR isbn IN
        SELECT e.isbn_13
          FROM book_editions e
         WHERE e.isbn_13 ~ '^[0-9]{13}$'
         GROUP BY e.isbn_13
        HAVING COUNT(DISTINCT e.book_id) > 1
    LOOP
        SELECT b.id INTO canonical_id
          FROM books b
          JOIN book_editions e ON e.book_id = b.id
         WHERE e.isbn_13 = isbn
         ORDER BY b.created_at ASC, b.id ASC
         LIMIT 1;

        FOR loser IN
            SELECT DISTINCT b.id
              FROM books b
              JOIN book_editions e ON e.book_id = b.id
             WHERE e.isbn_13 = isbn
               AND b.id <> canonical_id
        LOOP
            PERFORM _m2m_merge_book(canonical_id, loser.id);
        END LOOP;
    END LOOP;

    -- ISBN-10 pass on whatever's still duplicated
    FOR isbn IN
        SELECT e.isbn_10
          FROM book_editions e
         WHERE e.isbn_10 ~ '^[0-9]{9}[0-9Xx]$'
         GROUP BY e.isbn_10
        HAVING COUNT(DISTINCT e.book_id) > 1
    LOOP
        SELECT b.id INTO canonical_id
          FROM books b
          JOIN book_editions e ON e.book_id = b.id
         WHERE e.isbn_10 = isbn
         ORDER BY b.created_at ASC, b.id ASC
         LIMIT 1;

        FOR loser IN
            SELECT DISTINCT b.id
              FROM books b
              JOIN book_editions e ON e.book_id = b.id
             WHERE e.isbn_10 = isbn
               AND b.id <> canonical_id
        LOOP
            PERFORM _m2m_merge_book(canonical_id, loser.id);
        END LOOP;
    END LOOP;
END $$;

-- ── Phase 2: dedupe editions within a canonical book ───────────────────────
--
-- After phase 1, all editions that share an ISBN belong to the same book.
-- Collapse duplicates: earliest-created edition wins.

DO $$
DECLARE
    isbn TEXT;
    canonical_id UUID;
    loser RECORD;
BEGIN
    FOR isbn IN
        SELECT isbn_13
          FROM book_editions
         WHERE isbn_13 ~ '^[0-9]{13}$'
         GROUP BY isbn_13
        HAVING COUNT(*) > 1
    LOOP
        SELECT id INTO canonical_id
          FROM book_editions
         WHERE isbn_13 = isbn
         ORDER BY created_at ASC, id ASC
         LIMIT 1;

        FOR loser IN
            SELECT id FROM book_editions
             WHERE isbn_13 = isbn AND id <> canonical_id
        LOOP
            PERFORM _m2m_merge_edition(canonical_id, loser.id);
        END LOOP;
    END LOOP;

    FOR isbn IN
        SELECT isbn_10
          FROM book_editions
         WHERE isbn_10 ~ '^[0-9]{9}[0-9Xx]$'
         GROUP BY isbn_10
        HAVING COUNT(*) > 1
    LOOP
        SELECT id INTO canonical_id
          FROM book_editions
         WHERE isbn_10 = isbn
         ORDER BY created_at ASC, id ASC
         LIMIT 1;

        FOR loser IN
            SELECT id FROM book_editions
             WHERE isbn_10 = isbn AND id <> canonical_id
        LOOP
            PERFORM _m2m_merge_edition(canonical_id, loser.id);
        END LOOP;
    END LOOP;
END $$;

-- ── Cleanup ────────────────────────────────────────────────────────────────

DROP FUNCTION _m2m_merge_book(UUID, UUID);
DROP FUNCTION _m2m_merge_edition(UUID, UUID);
