-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725
--
-- Per-field timestamps for the most-contested soft fields on
-- user_book_interactions. Lets the new sync protocol resolve concurrent
-- offline edits with field-level last-write-wins instead of clobbering
-- the whole row.
--
-- Notes and reviews intentionally don't get per-field timestamps here.
-- They're handled via the append-only history tables in 000020.
-- Less-contested per-user fields (date_started, date_finished,
-- reread_count) fall back to row-level LWW via the existing updated_at.

ALTER TABLE user_book_interactions
    ADD COLUMN rating_updated_at      TIMESTAMPTZ,
    ADD COLUMN read_status_updated_at TIMESTAMPTZ,
    ADD COLUMN progress_updated_at    TIMESTAMPTZ,
    ADD COLUMN is_favorite_updated_at TIMESTAMPTZ;

-- Backfill from the row-level updated_at so existing data has a baseline
-- to compare against. Future writes set these explicitly per field.
UPDATE user_book_interactions
   SET rating_updated_at      = updated_at,
       read_status_updated_at = updated_at,
       progress_updated_at    = updated_at,
       is_favorite_updated_at = updated_at;
