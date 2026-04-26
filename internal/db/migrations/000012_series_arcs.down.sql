-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

DROP INDEX IF EXISTS idx_book_series_arc_id;
ALTER TABLE book_series DROP COLUMN IF EXISTS arc_id;
DROP TABLE IF EXISTS series_arcs;
