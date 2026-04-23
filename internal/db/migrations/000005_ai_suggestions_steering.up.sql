-- Steered suggestions: per-run user steering payload
-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- JSONB shape (all fields optional):
--   {
--     "author_ids": ["uuid", ...],
--     "series_ids": ["uuid", ...],
--     "genre_ids":  ["uuid", ...],
--     "tag_ids":    ["uuid", ...],
--     "notes":      "free-form text"
--   }
-- NULL means "no steering" — scheduled runs and unsteered manual runs.
ALTER TABLE ai_suggestion_runs ADD COLUMN steering JSONB;
