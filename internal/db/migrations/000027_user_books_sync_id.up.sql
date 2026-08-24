-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- user_books gains a surrogate id, for sync.
--
-- Its key is (user_id, book_id) and that is the right key: reading state is one
-- row per person per work, and a surrogate would be a second way to say the
-- same thing.
--
-- Sync needs one anyway. The protocol addresses rows by an opaque id that the
-- server emits in /sync/changes and resolves in /sync/apply, so the client only
-- ever echoes back what it was handed. Without a column to put there, moving
-- sync onto user_books would mean changing the shape iOS speaks, which puts a
-- schema change on the App Store's release schedule for no benefit.
--
-- So: a unique id alongside the natural key, used by exactly one caller. The
-- primary key does not move.

ALTER TABLE user_books ADD COLUMN IF NOT EXISTS id UUID NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX IF NOT EXISTS user_books_id_unique ON user_books (id);

-- Clients holding ids from before this migration will present ones that no
-- longer resolve. The protocol already answers that with not_found rather than
-- failing, and a client that gets not_found re-reads from /sync/changes, so the
-- worst case is one extra round trip rather than lost state.
