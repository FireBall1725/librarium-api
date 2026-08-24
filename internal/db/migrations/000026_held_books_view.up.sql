-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- held_books: what a library holds, one row per work.
--
-- 000025 replaced library_books and library_book_editions with copies, which is
-- one row per physical object. That is the right grain for the thing it
-- describes and the wrong grain for most reads: a list of books wants one row
-- per book, and joining copies directly would show a work twice the moment
-- someone owns two of it.
--
-- Rather than teach 60-odd call sites to collapse copies correctly, collapse
-- them once here. Every read that used to join library_books joins this
-- instead, and the multiplicity is handled in one place that can be tested
-- rather than in every query that can be forgotten.
--
-- The column list deliberately mirrors library_books, including a deleted_at
-- that is always NULL. Existing queries carry `lb.deleted_at IS NULL`
-- predicates, and keeping the column means the switch is a rename rather than a
-- rewrite of every WHERE clause. Live copies are the only ones the view exposes,
-- so the predicate is redundant rather than wrong.

CREATE VIEW held_books AS
SELECT DISTINCT ON (c.library_id, c.book_id)
       c.id,
       c.library_id,
       c.book_id,
       c.acquired_by AS added_by,
       c.created_at  AS added_at,
       NULL::timestamptz AS deleted_at
  FROM copies c
 WHERE c.deleted_at IS NULL
 ORDER BY c.library_id, c.book_id, c.created_at, c.id;

-- The id is the earliest copy's id, which is stable while that copy exists. It
-- is exposed because library_books had one and some reads select it, not
-- because it identifies the holding: a holding is now a set of copies, and the
-- thing worth addressing is a copy.

-- Supports the DISTINCT ON above, and every "does this library hold this work"
-- lookup that used to hit library_books' primary key.
CREATE INDEX IF NOT EXISTS copies_library_book_idx
    ON copies (library_id, book_id, created_at, id) WHERE deleted_at IS NULL;
