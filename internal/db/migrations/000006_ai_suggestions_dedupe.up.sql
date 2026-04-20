-- Prevent duplicate user-visible suggestions in the same batch.
-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- Clean up any existing duplicates before creating the unique index. Keep the
-- newest 'new' row per (user, type, normalized title); drop the older copies.
DELETE FROM ai_suggestions a
USING ai_suggestions b
WHERE a.status = 'new'
  AND b.status = 'new'
  AND a.user_id = b.user_id
  AND a.type = b.type
  AND lower(a.title) = lower(b.title)
  AND (a.created_at < b.created_at
       OR (a.created_at = b.created_at AND a.id < b.id));

-- One 'new' suggestion per (user, type, normalized title). Older runs'
-- dismissed/interested/added_to_library rows are left alone.
CREATE UNIQUE INDEX idx_ai_suggestions_unique_new
    ON ai_suggestions (user_id, type, lower(title))
    WHERE status = 'new';

-- Add 'cancelled' to the set of valid run statuses — no CHECK constraint to
-- alter, the column is VARCHAR(32), so nothing schema-level changes here.
-- Documented for future readers: 'running' | 'completed' | 'failed' | 'cancelled'.
