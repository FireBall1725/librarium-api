-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- series.genres was never emptied, so the free text is still there to read and
-- nothing is lost by dropping the join table.
--
-- The eight seeded genres stay. A genre a book has since been given is not this
-- migration's to take away, and an unused row in a vocabulary costs nothing.
DROP TABLE IF EXISTS series_genres;
