-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725

DROP INDEX IF EXISTS reading_sessions_user_book_open_idx;
ALTER TABLE reading_sessions DROP COLUMN IF EXISTS progress_updated_at;
