-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

DROP INDEX IF EXISTS lists_builtin_key_idx;
ALTER TABLE lists DROP COLUMN IF EXISTS builtin_key;
