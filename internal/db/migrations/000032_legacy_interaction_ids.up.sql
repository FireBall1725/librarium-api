-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- A forwarding address for interaction ids clients are still holding.
--
-- 000027 gave user_books a surrogate id for sync and generated fresh UUIDs for
-- it. Its own comment said clients presenting an old id would get not_found,
-- re-read from /sync/changes, and lose nothing but a round trip.
--
-- That is not what the shipping iOS client does. It treats not_found as
-- acknowledged and deletes the op, alongside applied and discarded_stale. So
-- every edit queued offline before an upgrade would be aimed at an id that no
-- longer resolves and thrown away: a rating set on a plane, a book marked read
-- in a basement, gone on reconnect with nothing said.
--
-- The old ids cannot simply be reused, because several per-edition rows can
-- collapse into one per-work row and only one of them could win. A forwarding
-- table keeps every one of them resolvable.
CREATE TABLE IF NOT EXISTS legacy_interaction_ids (
    legacy_id    UUID PRIMARY KEY,
    user_book_id UUID NOT NULL
);

-- Only meaningful where the old table still exists, which is every install that
-- upgraded rather than started fresh. Contract drops it with the rest.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables
                WHERE table_schema = current_schema() AND table_name = 'user_book_interactions') THEN
        INSERT INTO legacy_interaction_ids (legacy_id, user_book_id)
        SELECT ubi.id, ub.id
          FROM user_book_interactions ubi
          JOIN book_editions be ON be.id = ubi.book_edition_id
          JOIN user_books ub ON ub.user_id = ubi.user_id AND ub.book_id = be.book_id
        ON CONFLICT (legacy_id) DO NOTHING;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS legacy_interaction_ids_target_idx
    ON legacy_interaction_ids (user_book_id);
