-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- Reverse the M2M schema refactor.
--
-- NOTE: this down migration is LOSSY if multi-library membership was actually
-- used between the upgrade and the downgrade. Each book can only hold one
-- library_id on the scalar column, so we pick the earliest library_books
-- entry per book. Restore from a pre-migration backup if that matters.
--
-- If any book exists without a library_books row (e.g. a floating suggestion
-- book created after an upstream feature like suggestions-as-books ships),
-- the downgrade fails loudly rather than silently dropping those rows.

-- ── Put the scalar columns back ────────────────────────────────────────────

ALTER TABLE books         ADD COLUMN library_id  UUID REFERENCES libraries(id) ON DELETE CASCADE;
ALTER TABLE books         ADD COLUMN added_by    UUID REFERENCES users(id);

ALTER TABLE book_editions ADD COLUMN copy_count  INTEGER NOT NULL DEFAULT 1;
ALTER TABLE book_editions ADD COLUMN acquired_at DATE;

-- ── Repopulate from junctions ──────────────────────────────────────────────

-- books.library_id + added_by — pick the earliest-added library_books row
-- per book. DISTINCT ON respects the ORDER BY to choose one row per book_id.
UPDATE books
SET library_id = lb.library_id,
    added_by   = lb.added_by
FROM (
    SELECT DISTINCT ON (book_id) book_id, library_id, added_by
    FROM library_books
    ORDER BY book_id, added_at ASC, id ASC
) lb
WHERE books.id = lb.book_id;

-- Fail loudly on floating books — we have no library to downgrade them to.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM books WHERE library_id IS NULL) THEN
        RAISE EXCEPTION 'Cannot downgrade: some books have no library_books row (likely floating/suggestion books). Restore from pre-migration backup.';
    END IF;
END $$;

-- Now the column can be NOT NULL again.
ALTER TABLE books ALTER COLUMN library_id SET NOT NULL;

-- Restore the library_id index.
CREATE INDEX idx_books_library_id ON books(library_id);

-- book_editions.copy_count — sum across libraries (total copies owned).
-- acquired_at — earliest acquisition date across libraries.
UPDATE book_editions
SET copy_count  = COALESCE(lbe.total_copies, 1),
    acquired_at = lbe.earliest_acquired
FROM (
    SELECT book_edition_id,
           SUM(copy_count)   AS total_copies,
           MIN(acquired_at)  AS earliest_acquired
    FROM library_book_editions
    GROUP BY book_edition_id
) lbe
WHERE book_editions.id = lbe.book_edition_id;

-- ── Drop junctions ─────────────────────────────────────────────────────────

DROP TABLE library_book_editions;
DROP TABLE library_books;
