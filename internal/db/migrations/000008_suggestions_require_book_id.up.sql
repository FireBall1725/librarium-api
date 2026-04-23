-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- Suggestions-as-Books: ai_suggestions always references a real book.
--
-- Backfills every buy-type suggestion that's missing a book_id with either
-- an existing edition's book_id (ISBN match, post-M2M dedupe) or a newly
-- created floating book + edition derived from the suggestion's denormalised
-- title/author/isbn/cover_url fields. Then drops those denorm columns and
-- makes book_id NOT NULL so the worker can't insert a dangling suggestion.
--
-- read_next suggestions already have book_id populated (they reference a
-- book the user owns); they're untouched here.
--
-- This is not reversible in any meaningful way — once the denorm columns
-- are dropped, down-migrating just re-adds them empty. See down.sql.

-- ── Backfill step 1: reuse existing editions where the ISBN matches ────────
-- A buy suggestion whose ISBN already resolves to an edition in the catalog
-- (possibly because another user was suggested the same book, or because
-- the book got imported into some library after the suggestion was made)
-- gets wired up to that edition's book_id.

UPDATE ai_suggestions s
   SET book_id         = e.book_id,
       book_edition_id = e.id
  FROM book_editions e
 WHERE s.type = 'buy'
   AND s.book_id IS NULL
   AND s.isbn IS NOT NULL
   AND s.isbn <> ''
   AND (e.isbn_13 = s.isbn OR e.isbn_10 = s.isbn);

-- ── Backfill step 2: create floating books for everything else ─────────────
-- Pick a default media_type_id (prefer "novel", fall back to the first one).

DO $$
DECLARE
    default_media_type_id UUID;
    sugg RECORD;
    new_book_id UUID;
    new_edition_id UUID;
BEGIN
    SELECT id INTO default_media_type_id
      FROM media_types
     WHERE name = 'novel'
     LIMIT 1;
    IF default_media_type_id IS NULL THEN
        SELECT id INTO default_media_type_id FROM media_types ORDER BY name LIMIT 1;
    END IF;
    IF default_media_type_id IS NULL THEN
        RAISE EXCEPTION 'No media types defined; cannot backfill floating books';
    END IF;

    FOR sugg IN
        SELECT id, title, COALESCE(NULLIF(isbn,''),'') AS isbn
          FROM ai_suggestions
         WHERE type = 'buy' AND book_id IS NULL
    LOOP
        new_book_id := gen_random_uuid();
        new_edition_id := gen_random_uuid();

        INSERT INTO books (id, title, media_type_id)
        VALUES (new_book_id, sugg.title, default_media_type_id);

        INSERT INTO book_editions (id, book_id, format, isbn_13, is_primary)
        VALUES (
            new_edition_id,
            new_book_id,
            'paperback',
            NULLIF(sugg.isbn, ''),
            TRUE
        );

        UPDATE ai_suggestions
           SET book_id = new_book_id,
               book_edition_id = new_edition_id
         WHERE id = sugg.id;
    END LOOP;
END $$;

-- ── Lock it in ─────────────────────────────────────────────────────────────
-- At this point every suggestion should have a book_id. Anything left over
-- is a bug — fail loudly rather than silently dropping rows.

DO $$
DECLARE
    orphan_count INT;
BEGIN
    SELECT COUNT(*) INTO orphan_count FROM ai_suggestions WHERE book_id IS NULL;
    IF orphan_count > 0 THEN
        RAISE EXCEPTION
          'Backfill left % ai_suggestions rows with null book_id; refusing to make column NOT NULL',
          orphan_count;
    END IF;
END $$;

ALTER TABLE ai_suggestions
    ALTER COLUMN book_id SET NOT NULL;

-- Book deletion should cascade through suggestions — previously SET NULL,
-- which left dangling denormalised rows around. With book_id NOT NULL that's
-- no longer an option. Swap to CASCADE.
ALTER TABLE ai_suggestions
    DROP CONSTRAINT ai_suggestions_book_id_fkey,
    ADD CONSTRAINT ai_suggestions_book_id_fkey
      FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE;

-- ── Drop the denormalised columns ──────────────────────────────────────────
-- Responses now hydrate from the joined books row. The partial unique index
-- on (user_id, type, lower(title)) auto-drops with the title column.

ALTER TABLE ai_suggestions
    DROP COLUMN title,
    DROP COLUMN author,
    DROP COLUMN isbn,
    DROP COLUMN cover_url;

-- Replace the dropped unique index with one keyed on book_id — same
-- "one 'new' suggestion per (user, type, book)" semantic.
CREATE UNIQUE INDEX idx_ai_suggestions_unique_new
    ON ai_suggestions (user_id, type, book_id)
    WHERE status = 'new';
