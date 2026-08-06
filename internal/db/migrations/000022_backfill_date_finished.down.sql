-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725
--
-- Deliberately a no-op.
--
-- The up migration writes a date onto rows that had none. Reversing it would
-- mean nulling date_finished for read books, and nothing here distinguishes a
-- date this backfill wrote from one the user typed in afterwards. That query
-- would destroy real data to undo a guess.
--
-- Rolling back past this point leaves the backfilled dates in place. They stay
-- harmless: the pre-000022 dashboard reads COALESCE(date_finished, updated_at)
-- and the backfilled value is the same value that COALESCE was already
-- returning, so the numbers do not change on the way down either.

SELECT 1;
