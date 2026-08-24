-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725

-- progress_updated_at: when the progress on this pass last changed.
--
-- Sync is per-field last-writer-wins, so every field it carries needs its own
-- timestamp to compare against. Progress moved from the old interactions row
-- (which had one) to reading_sessions (which did not), so the sync path had
-- nothing to compare and rejected the field outright.
--
-- Rejecting was worse than it looked. The iOS client treats a rejected op as
-- acknowledged and deletes it, so a reader who set their page count watched it
-- save locally and never leave the device. That is silent data loss, and it is
-- the shipping client, not a hypothetical one.
--
-- Nullable, with no backfill: a session nobody has recorded progress against
-- has no such moment, and inventing created_at would claim progress was set
-- when the session was opened.
ALTER TABLE reading_sessions ADD COLUMN IF NOT EXISTS progress_updated_at TIMESTAMPTZ;

-- The sync read finds the pass a book's progress belongs to. One open session
-- per book is the common case; the index keeps it a lookup rather than a scan.
CREATE INDEX IF NOT EXISTS reading_sessions_user_book_open_idx
    ON reading_sessions (user_id, book_id, created_at DESC)
 WHERE finished_at IS NULL;
