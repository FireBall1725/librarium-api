-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

DROP INDEX IF EXISTS idx_user_book_interactions_deleted_at;
DROP INDEX IF EXISTS idx_loans_deleted_at;
DROP INDEX IF EXISTS idx_shelves_deleted_at;
DROP INDEX IF EXISTS idx_series_deleted_at;
DROP INDEX IF EXISTS idx_library_books_deleted_at;
DROP INDEX IF EXISTS idx_library_book_editions_deleted_at;
DROP INDEX IF EXISTS idx_book_shelves_deleted_at;
DROP INDEX IF EXISTS idx_book_tags_deleted_at;
DROP INDEX IF EXISTS idx_library_memberships_deleted_at;
DROP INDEX IF EXISTS idx_tags_deleted_at;

ALTER TABLE user_book_interactions DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE loans                  DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE shelves                DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE series                 DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE library_books          DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE library_book_editions  DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE book_shelves           DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE book_tags              DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE library_memberships    DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE tags                   DROP COLUMN IF EXISTS deleted_at;
