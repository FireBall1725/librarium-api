-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

DROP INDEX IF EXISTS series_volumes_book_idx;
ALTER TABLE series_volumes DROP COLUMN IF EXISTS book_id;
