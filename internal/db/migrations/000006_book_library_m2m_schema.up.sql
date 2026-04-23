-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- Book ↔ Library many-to-many refactor: schema phase.
--
-- Moves library ownership off the `books` table and onto two junctions:
--   library_books         — work-level ownership ("does this library hold
--                            the book at all")
--   library_book_editions — edition-level copy counts ("this library has
--                            2 hardbacks and 1 audiobook")
--
-- This migration is additive + subtractive: it creates the new structure,
-- backfills it from the current scalar columns, then drops those columns.
-- It does NOT dedupe ISBN-duplicate books/editions across libraries — that
-- is 000007.
--
-- `book_editions.is_primary` stays on the edition — it's a property of the
-- manifestation (e.g. "this is the 1st-print hardback"), not a per-library
-- preference.

-- ── Junctions ──────────────────────────────────────────────────────────────

CREATE TABLE library_books (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    book_id    UUID        NOT NULL REFERENCES books(id)     ON DELETE CASCADE,
    added_by   UUID        REFERENCES users(id),
    added_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (library_id, book_id)
);

CREATE INDEX idx_library_books_library_id ON library_books(library_id);
CREATE INDEX idx_library_books_book_id    ON library_books(book_id);

CREATE TABLE library_book_editions (
    library_id      UUID        NOT NULL REFERENCES libraries(id)     ON DELETE CASCADE,
    book_edition_id UUID        NOT NULL REFERENCES book_editions(id) ON DELETE CASCADE,
    copy_count      INTEGER     NOT NULL DEFAULT 1,
    acquired_at     DATE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (library_id, book_edition_id)
);

CREATE INDEX idx_lbe_book_edition_id ON library_book_editions(book_edition_id);

-- ── Backfill ───────────────────────────────────────────────────────────────

-- One library_books row per existing book, carrying over the current scalar
-- ownership fields. The book's created_at becomes added_at — close enough,
-- and we don't have a separate "added to library" timestamp today.
INSERT INTO library_books (library_id, book_id, added_by, added_at)
SELECT library_id, id, added_by, created_at
FROM books;

-- One library_book_editions row per existing edition, scoped to its book's
-- library. Carries over copy_count and acquired_at.
INSERT INTO library_book_editions (library_id, book_edition_id, copy_count, acquired_at)
SELECT b.library_id, e.id, e.copy_count, e.acquired_at
FROM book_editions e
JOIN books b ON b.id = e.book_id;

-- ── Drop columns that moved to junctions ───────────────────────────────────
-- idx_books_library_id drops automatically with the column.

ALTER TABLE books            DROP COLUMN library_id;
ALTER TABLE books            DROP COLUMN added_by;

ALTER TABLE book_editions    DROP COLUMN copy_count;
ALTER TABLE book_editions    DROP COLUMN acquired_at;
