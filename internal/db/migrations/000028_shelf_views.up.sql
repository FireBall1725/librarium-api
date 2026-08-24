-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- library_shelves and library_shelf_books: the shelf-shaped view of lists.
--
-- 000025 merged shelves and saved views into lists, because they were never
-- distinguished by ownership but by how membership is decided. The shelf routes
-- still exist and still speak the old shape, so rather than leave them writing a
-- table nothing reads, they read and write lists through these.
--
-- A shelf is a manual list shared with a library, which is exactly what the
-- migration turned every existing shelf into. A smart list has no shelf-shaped
-- equivalent and is deliberately absent here: it computes its own membership,
-- so the shelf API could not add a book to one anyway.
--
-- Column lists mirror the old tables, including deleted_at columns that are
-- always NULL, so the switch is a rename at the call sites rather than a
-- rewrite of every WHERE clause.

CREATE VIEW library_shelves AS
SELECT l.id,
       l.shared_library_id AS library_id,
       l.name,
       l.description,
       l.color,
       l.icon,
       l.display_order,
       l.owner_user_id AS created_by,
       l.created_at,
       l.updated_at,
       NULL::timestamptz AS deleted_at
  FROM lists l
 WHERE l.kind = 'manual' AND l.visibility = 'library';

CREATE VIEW library_shelf_books AS
SELECT lb.list_id AS shelf_id,
       lb.book_id,
       lb.added_at,
       NULL::uuid AS added_by,
       NULL::timestamptz AS deleted_at
  FROM list_books lb
  JOIN lists l ON l.id = lb.list_id
 WHERE l.kind = 'manual' AND l.visibility = 'library';

CREATE INDEX IF NOT EXISTS lists_shelf_shape_idx
    ON lists (shared_library_id, display_order, name)
 WHERE kind = 'manual' AND visibility = 'library';
