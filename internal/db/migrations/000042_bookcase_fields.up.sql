-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725

-- A place can say it's a bookcase: how many shelves it has and which end is
-- shelf 1. The names of the shelves inside it can't say either, since a shelf
-- nobody has filed anything on yet may have no row, and "Shelf 1" reads the
-- same from the top or the bottom.
--
-- Setting shelf_count is what makes a place a bookcase. No kind column and no
-- bookcase table: copies already point at copy_locations, and places already
-- nest to any depth, so a bookcase is just a place with these filled in.
ALTER TABLE copy_locations
    ADD COLUMN IF NOT EXISTS shelf_count SMALLINT
        CHECK (shelf_count IS NULL OR shelf_count BETWEEN 1 AND 50),
    ADD COLUMN IF NOT EXISTS shelf_numbering TEXT
        CHECK (shelf_numbering IS NULL OR shelf_numbering IN ('top_down', 'bottom_up'));
