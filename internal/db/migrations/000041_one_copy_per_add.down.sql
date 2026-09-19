-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725

-- Brings back exactly the copies the up migration removed.
UPDATE copies
   SET deleted_at = NULL, updated_at = NOW()
 WHERE id IN (SELECT id FROM copies_removed_by_41);

DROP TABLE IF EXISTS copies_removed_by_41;
