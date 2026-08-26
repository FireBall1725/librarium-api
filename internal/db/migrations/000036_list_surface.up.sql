-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- Which page a saved view lands on.
--
-- A stored filter has always been a URL query string: listQuery returns it and
-- every caller hands it to URLSearchParams. Nothing about that is specific to
-- books. What was specific to books is that the rail had no way to say where a
-- row goes, so it assumed, and the assumption was the only thing stopping a
-- view from standing for a shelf of series as easily as a shelf of books.
--
-- One column says it instead. Views keep one implementation of sharing,
-- ordering, renaming, icons and permissions; a second table would have meant a
-- second copy of all of it to answer a question one word can answer.
ALTER TABLE lists
    ADD COLUMN IF NOT EXISTS surface TEXT NOT NULL DEFAULT 'books';

-- Constrained rather than free text. A filter is already a query language with
-- no schema, which is what filter_version exists to survive; the surface is the
-- one part of a view that must not be guessable, because getting it wrong sends
-- someone to a page that silently matches nothing.
ALTER TABLE lists
    DROP CONSTRAINT IF EXISTS lists_surface_is_known;
ALTER TABLE lists
    ADD CONSTRAINT lists_surface_is_known CHECK (surface IN ('books', 'series'));

-- A hand-picked set is a set of books: list_books is the only membership table
-- there is. Until there is a list_series to match, a manual view is a book
-- view, and saying so here stops a series view being created in a shape nothing
-- can read.
ALTER TABLE lists
    DROP CONSTRAINT IF EXISTS lists_manual_is_books;
ALTER TABLE lists
    ADD CONSTRAINT lists_manual_is_books CHECK (kind = 'smart' OR surface = 'books');

-- Nothing is backfilled beyond the default. Every row that exists today is a
-- book view, which is exactly what 'books' says, and a default rather than an
-- UPDATE means an instance with a large lists table does not rewrite it.

-- The built-in key is unique per user, and both surfaces ship a Default view.
-- Keying on the pair lets each page open on its own without one clobbering the
-- other's seed.
DROP INDEX IF EXISTS lists_builtin_key_idx;
CREATE UNIQUE INDEX IF NOT EXISTS lists_builtin_key_idx
    ON lists (owner_user_id, surface, builtin_key) WHERE builtin_key IS NOT NULL;
