-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725

-- Remove the second copy that adding a book used to make.
--
-- From the tier schema (migration 25) until this fix, adding a book recorded
-- the library holding it with a copy that had no edition, then added another
-- copy for the edition beside it, so every book added showed two copies
-- (librarium-ios #81). The code now gives the edition to that first copy.
--
-- A copy is removed only when all of this holds, which is the bug's exact
-- shape and nothing a person made:
--   * it has no edition and isn't already deleted,
--   * the same library has a copy of the same book that does have an edition,
--     created within a minute of it (the two came from one add),
--   * it carries nothing anyone typed: no condition, notes, price, place,
--     acquisition date or source, and it isn't marked signed.
--
-- Soft delete, like every other copy removal. The ids go in a table so the
-- down migration restores exactly these and nothing else.
CREATE TABLE IF NOT EXISTS copies_removed_by_41 (id UUID PRIMARY KEY);

INSERT INTO copies_removed_by_41 (id)
SELECT c.id
  FROM copies c
 WHERE c.edition_id IS NULL
   AND c.deleted_at IS NULL
   AND NOT c.is_signed
   AND c.condition IS NULL
   AND c.notes = ''
   AND c.price_minor IS NULL
   AND c.location_id IS NULL
   AND c.acquired_at IS NULL
   AND c.acquired_from = ''
   AND EXISTS (
       SELECT 1 FROM copies e
        WHERE e.library_id = c.library_id
          AND e.book_id = c.book_id
          AND e.edition_id IS NOT NULL
          AND e.deleted_at IS NULL
          AND abs(extract(epoch FROM e.created_at - c.created_at)) < 60)
ON CONFLICT DO NOTHING;

UPDATE copies
   SET deleted_at = NOW(), updated_at = NOW()
 WHERE id IN (SELECT id FROM copies_removed_by_41);
