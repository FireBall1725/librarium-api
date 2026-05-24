-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725
--
-- Tracks the devices a user has synced from. Used for two things:
--
-- 1. Tombstone GC. The safe cutoff for hard-deleting a tombstone is
--    min(last_synced_through) across all of the user's active clients.
--    Inactive clients (no last_seen_at within the retention window)
--    drop out of the calculation so a forgotten device doesn't pin
--    tombstones forever.
--
-- 2. Sync delta filtering. The server omits ops authored by the
--    requesting client_id from its /sync/changes response since the
--    client already has them in its outbox.
--
-- Lite users have a single client; their cutoff is just that one
-- device's last_synced_through.

CREATE TABLE sync_clients (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_name          VARCHAR(128),
    last_seen_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_synced_through  TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sync_clients_user_id      ON sync_clients(user_id);
CREATE INDEX idx_sync_clients_last_seen_at ON sync_clients(last_seen_at);
