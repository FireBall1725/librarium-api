-- SPDX-License-Identifier: AGPL-3.0-only

DROP INDEX IF EXISTS idx_contributors_sort_name;

ALTER TABLE contributors
    DROP COLUMN IF EXISTS is_corporate,
    DROP COLUMN IF EXISTS sort_name;
