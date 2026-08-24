-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- builtin_key: which shipped list this row is, if it is one at all.
--
-- 000024 gave saved views three booleans, built_in, hidden and permanent, and
-- 000025 folded views into lists without them. They are not properties of a
-- row, they are properties of a definition this release shipped: the Default
-- view is hidden because of what it is for, not because someone set a flag on
-- it. Storing them would let a rename make Default deletable, and would freeze
-- the answers at the moment a user was first seeded, so a later release that
-- unhid a built-in would only affect accounts created after it.
--
-- One nullable text key instead. Non-null means the row is the shipped list of
-- that name and the flags are looked up in Go, where they can change with a
-- release rather than a migration. Null means a person made it, which is the
-- default and needs no backfill.
ALTER TABLE lists ADD COLUMN IF NOT EXISTS builtin_key TEXT;

-- Also the idempotency key for seeding: seeding is an insert that must be safe
-- to run on every request, because "has this user been seeded" has no other
-- honest answer once a user is allowed to delete a built-in.
CREATE UNIQUE INDEX IF NOT EXISTS lists_builtin_key_idx
    ON lists (owner_user_id, builtin_key) WHERE builtin_key IS NOT NULL;
