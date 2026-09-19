-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725

-- The answers are a cache of what providers said; the edition's own fields
-- hold what was picked, so dropping them loses no choice anyone made.
DROP TABLE IF EXISTS edition_provider_answers;
