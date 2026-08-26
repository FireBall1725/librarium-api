-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- The bridge between what a provider knows and what the collection holds.
--
-- series_volumes has held provider data since migration 10, and a background
-- checker has been refreshing it every 24 hours since. Four hundred and forty
-- eight volumes are on record across sixty-one series, and not one of them can
-- surface anywhere, because nothing turns a volume into a book. The ownership
-- facet already understands the idea: a gap is a book recorded against one of
-- your series that no library holds, and it is already a row in the rail
-- reading "Missing volume". The detection works. The data to feed it is
-- sitting in this table. The join between them did not exist.
--
-- book_id is that join. It says "this volume is that book", and it serves two
-- directions at once: promotion writes it when a volume becomes a book, and
-- matching writes it when a volume turns out to be a book already on the shelf.
-- Nullable, because a volume nobody has promoted or matched yet is the normal
-- state and the only honest way to record it.
ALTER TABLE series_volumes
    ADD COLUMN IF NOT EXISTS book_id UUID REFERENCES books(id) ON DELETE SET NULL;

-- ON DELETE SET NULL rather than CASCADE. Deleting a book must not silently
-- take the provider's record of the volume with it: the volume still exists in
-- the world, and the next sync would put the row back anyway, minus whatever
-- else was on it.

-- One book per volume, and one volume per book. Without this a re-run that
-- failed halfway could promote the same volume twice, which is the failure the
-- whole column exists to prevent.
CREATE UNIQUE INDEX IF NOT EXISTS series_volumes_book_idx
    ON series_volumes (book_id) WHERE book_id IS NOT NULL;

-- Nothing is backfilled. Matching a volume to a book someone already owns is a
-- judgement about titles and numbering, not a fact this migration can derive,
-- and guessing wrong here attaches a provider's record to the wrong object in
-- somebody's catalogue. The matcher does it, deliberately, and reports what it
-- did.
