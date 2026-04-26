-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

ALTER TABLE series_arcs
    DROP COLUMN IF EXISTS vol_end,
    DROP COLUMN IF EXISTS vol_start;
