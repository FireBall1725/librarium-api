-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)
--
-- Saved views, moved off the browser and onto the server.
--
-- A view is a named set of filters the reader saved: what Books opens on, and
-- the entries under Your views in the rail. They lived in localStorage, which
-- made them per-browser rather than per-person — the same account saw a
-- different set on a laptop and a phone, clearing site data lost them, and
-- nothing could seed or back them up. They belong to the user, so they belong
-- here.
--
-- id is TEXT, not UUID, because the ids the browser already minted are carried
-- across unchanged: the built-ins are named ('default', 'reading', 'unread'),
-- and user-made ones look like 'v1a2b3c4'. Keeping them means the one-time
-- import from localStorage is an insert rather than a remapping, and any state
-- keyed on a view id stays valid.
CREATE TABLE user_views (
    user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    id            TEXT        NOT NULL,
    name          TEXT        NOT NULL,
    -- Query string exactly as Books puts it in the URL, e.g. "status=reading".
    params        TEXT        NOT NULL DEFAULT '',
    -- 'grid' or 'rows'. Covers suit a shelf you are browsing, rows a list you
    -- are working through, which is why it belongs to the view rather than
    -- being one global toggle.
    layout        TEXT        NOT NULL DEFAULT 'grid',
    -- Icon name from the client's own set. Null falls back to a per-id map and
    -- then to a generic icon, so a view saved before icons existed still draws.
    icon          TEXT,
    -- Shipped with the product. Only drives first-run seeding; still deletable.
    built_in      BOOLEAN     NOT NULL DEFAULT FALSE,
    -- Never listed in the rail. Only the Default view is.
    hidden        BOOLEAN     NOT NULL DEFAULT FALSE,
    -- Cannot be deleted: Books has to have something to open on. This also
    -- guarantees a user who has been seeded always has at least one row, which
    -- is what lets "no rows" mean "never seeded" without a separate flag.
    permanent     BOOLEAN     NOT NULL DEFAULT FALSE,
    display_order INTEGER     NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, id)
);

-- The rail reads every view for one user on each page, ordered.
CREATE INDEX idx_user_views_user_order ON user_views (user_id, display_order, created_at);
