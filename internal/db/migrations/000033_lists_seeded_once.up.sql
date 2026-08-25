-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- lists_seeded_at: when this person was given the example lists.
--
-- The lists shipped with the product were re-inserted on every read, because
-- "has this user been seeded" had no honest answer without somewhere to record
-- it. That made them undeletable in practice: removing one worked, and the next
-- page load put it straight back.
--
-- They are examples, not fixtures. A reader should be able to rename Five stars,
-- point it somewhere else, or throw it away and have that stick. Recording the
-- moment they were handed over is what lets seeding happen once and then leave
-- them alone.
--
-- Nullable: NULL means never seeded, which is exactly what an account created
-- before this migration should look like, so anyone who has not yet been given
-- the examples still gets them.
ALTER TABLE users ADD COLUMN IF NOT EXISTS lists_seeded_at TIMESTAMPTZ;

-- Anyone who already holds a built-in has plainly been seeded. Without this
-- every existing account would be seeded a second time, resurrecting the exact
-- rows they may have just deleted.
UPDATE users u
   SET lists_seeded_at = NOW()
 WHERE u.lists_seeded_at IS NULL
   AND EXISTS (SELECT 1 FROM lists l
                WHERE l.owner_user_id = u.id AND l.builtin_key IS NOT NULL);
