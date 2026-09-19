-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725

ALTER TABLE copy_locations
    DROP COLUMN IF EXISTS shelf_numbering,
    DROP COLUMN IF EXISTS shelf_count;
