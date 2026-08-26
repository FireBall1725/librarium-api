-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- Series views cannot survive the column that says they are series views, and
-- leaving them would make them book views matching nothing.
DELETE FROM lists WHERE surface <> 'books';

DROP INDEX IF EXISTS lists_builtin_key_idx;
ALTER TABLE lists DROP CONSTRAINT IF EXISTS lists_manual_is_books;
ALTER TABLE lists DROP CONSTRAINT IF EXISTS lists_surface_is_known;
ALTER TABLE lists DROP COLUMN IF EXISTS surface;
CREATE UNIQUE INDEX IF NOT EXISTS lists_builtin_key_idx
    ON lists (owner_user_id, builtin_key) WHERE builtin_key IS NOT NULL;
