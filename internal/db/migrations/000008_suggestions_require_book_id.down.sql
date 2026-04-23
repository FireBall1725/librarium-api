-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- Reverses 000008: drops the NOT NULL + CASCADE on ai_suggestions.book_id,
-- re-adds the denormalised title/author/isbn/cover_url columns, and copies
-- data back from the joined books + editions rows where possible.
--
-- NOTE: this down-migration will copy title from the current books.title
-- (which may have been edited/enriched since the suggestion was made) and
-- pick the first edition's ISBN. Cover URL is not restored (it was a
-- transient external URL at suggestion time and we no longer store it).
-- If exact historical fidelity matters, restore from a pre-migration dump.

-- Restore the FK to the pre-000008 ON DELETE SET NULL behaviour.
ALTER TABLE ai_suggestions
    DROP CONSTRAINT ai_suggestions_book_id_fkey,
    ADD CONSTRAINT ai_suggestions_book_id_fkey
      FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE SET NULL;

ALTER TABLE ai_suggestions
    ALTER COLUMN book_id DROP NOT NULL;

-- Re-add the denorm columns (all nullable, as they were originally).
ALTER TABLE ai_suggestions
    ADD COLUMN title     TEXT NOT NULL DEFAULT '',
    ADD COLUMN author    TEXT,
    ADD COLUMN isbn      TEXT,
    ADD COLUMN cover_url TEXT;

-- Drop the book_id-based unique index and restore the title-based one.
DROP INDEX IF EXISTS idx_ai_suggestions_unique_new;

-- Best-effort repopulation from the joined book/edition rows.
UPDATE ai_suggestions s
   SET title  = b.title,
       author = COALESCE(
                  (SELECT c.name
                     FROM book_contributors bc
                     JOIN contributors c ON c.id = bc.contributor_id
                    WHERE bc.book_id = b.id AND bc.role = 'author'
                    ORDER BY bc.display_order
                    LIMIT 1),
                  NULL),
       isbn   = COALESCE(
                  (SELECT COALESCE(NULLIF(e.isbn_13,''), NULLIF(e.isbn_10,''))
                     FROM book_editions e
                    WHERE e.book_id = b.id
                    ORDER BY e.is_primary DESC, e.created_at ASC
                    LIMIT 1),
                  NULL)
  FROM books b
 WHERE s.book_id = b.id;

-- Restore the title-based unique index.
CREATE UNIQUE INDEX idx_ai_suggestions_unique_new
    ON ai_suggestions (user_id, type, lower(title))
    WHERE status = 'new';
