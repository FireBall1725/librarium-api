-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- Where each person put each view in their own sidebar.
--
-- lists.display_order is one number on one row, so it could only ever describe
-- one arrangement. That was fine while every view belonged to the person
-- looking at it, and stopped being fine the moment a view shared into a library
-- appeared in someone else's rail: dragging it either moved it for everybody or
-- failed, and it failed. The reorder is a PATCH on the list, the list is
-- guarded by ownership, and the client swallowed the 404, so the row sprang
-- back on the next load with nothing said.
--
-- Order is a property of a sidebar, not of a view. This table says so.
CREATE TABLE IF NOT EXISTS list_order_overrides (
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    list_id       UUID NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
    display_order INTEGER NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, list_id)
);

-- The read is "every view this person can see, in their order", so the index
-- covers the join and the sort together.
CREATE INDEX IF NOT EXISTS list_order_overrides_user_idx
    ON list_order_overrides (user_id, display_order);

-- Nothing is backfilled on purpose. An absent row means "wherever the owner put
-- it", which is the right starting point for a view someone has just been given
-- and the right answer for every existing account, none of which has expressed
-- an opinion yet. lists.display_order keeps its meaning as that seed rather
-- than becoming dead weight.
