-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725
--
-- Gives every already-read book a date_finished, so the dashboard can stop
-- falling back to updated_at at query time.
--
-- The dashboard currently reads COALESCE(date_finished, updated_at). That is
-- wrong as a query: updated_at moves whenever the row is written for any
-- reason, so an import or a metadata refresh silently drags an old book into
-- "read this year", and does it again on the next write. Nothing sets
-- date_finished when a book is marked read from the UI, so a large share of
-- rows depend on that fallback.
--
-- Dropping the fallback without this backfill would empty out "read this
-- year", the twelve-month sparkline, and "recently finished" for anyone who
-- marked books read without typing a date.
--
-- Using updated_at once, here, is a different thing from using it at query
-- time. This is a one-time best guess at history that was never recorded,
-- frozen at the value the dashboard is already showing today, so the numbers
-- do not move. Query-time COALESCE keeps re-deciding the answer forever.
--
-- Only rows that are actually finished and have no date are touched. A row
-- with a real date_finished is left alone.

UPDATE user_book_interactions
SET date_finished = updated_at::date
WHERE read_status = 'read'
  AND date_finished IS NULL;
