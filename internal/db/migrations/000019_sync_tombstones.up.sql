-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725
--
-- Tombstones for the sync protocol. Adds a deleted_at column to every
-- table whose row deletions need to propagate to offline clients via the
-- sync delta endpoint. Rows with deleted_at set are soft-deleted; reads
-- should filter WHERE deleted_at IS NULL.
--
-- A nightly GC will hard-delete tombstones once every active client has
-- synced past their deleted_at (see plans/sync-protocol.md).
--
-- Existing queries continue to work unchanged at this point. Adopting
-- the filter happens alongside the sync endpoints in a follow-up.

ALTER TABLE user_book_interactions ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE loans                  ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE shelves                ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE series                 ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE library_books          ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE library_book_editions  ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE book_shelves           ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE book_tags              ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE library_memberships    ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE tags                   ADD COLUMN deleted_at TIMESTAMPTZ;

-- Partial indexes on deleted_at IS NOT NULL keep the sync delta query
-- ("give me everything deleted since T") fast without bloating the
-- index. Most rows are never deleted, so the partial index stays small.
CREATE INDEX idx_user_book_interactions_deleted_at ON user_book_interactions(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX idx_loans_deleted_at                  ON loans(deleted_at)                  WHERE deleted_at IS NOT NULL;
CREATE INDEX idx_shelves_deleted_at                ON shelves(deleted_at)                WHERE deleted_at IS NOT NULL;
CREATE INDEX idx_series_deleted_at                 ON series(deleted_at)                 WHERE deleted_at IS NOT NULL;
CREATE INDEX idx_library_books_deleted_at          ON library_books(deleted_at)          WHERE deleted_at IS NOT NULL;
CREATE INDEX idx_library_book_editions_deleted_at  ON library_book_editions(deleted_at)  WHERE deleted_at IS NOT NULL;
CREATE INDEX idx_book_shelves_deleted_at           ON book_shelves(deleted_at)           WHERE deleted_at IS NOT NULL;
CREATE INDEX idx_book_tags_deleted_at              ON book_tags(deleted_at)              WHERE deleted_at IS NOT NULL;
CREATE INDEX idx_library_memberships_deleted_at    ON library_memberships(deleted_at)    WHERE deleted_at IS NOT NULL;
CREATE INDEX idx_tags_deleted_at                   ON tags(deleted_at)                   WHERE deleted_at IS NOT NULL;
