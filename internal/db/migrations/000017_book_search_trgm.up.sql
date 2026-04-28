-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725
--
-- Fuzzy "did you mean" support for book search. Enables the pg_trgm
-- extension and adds a GIN index on lower(title || ' ' || subtitle) so
-- a similarity match is fast even on large libraries.
--
-- The search handler falls through to a similarity match when the
-- literal `q` returns 0 hits and surfaces the top suggestion.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS idx_books_title_trgm
    ON books USING gin (lower(title || ' ' || COALESCE(subtitle, '')) gin_trgm_ops);
