-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

ALTER TABLE user_book_interactions
    DROP COLUMN IF EXISTS rating_updated_at,
    DROP COLUMN IF EXISTS read_status_updated_at,
    DROP COLUMN IF EXISTS progress_updated_at,
    DROP COLUMN IF EXISTS is_favorite_updated_at;
