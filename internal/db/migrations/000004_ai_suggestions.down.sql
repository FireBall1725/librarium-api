-- Down migration for AI suggestions
-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

DROP TABLE IF EXISTS ai_blocked_items;
DROP TABLE IF EXISTS ai_suggestions;
DROP TABLE IF EXISTS ai_suggestion_runs;
DROP TABLE IF EXISTS user_ai_settings;

-- Remove instance_settings entries owned by this feature
DELETE FROM instance_settings WHERE key LIKE 'ai:%';
