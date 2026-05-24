-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

DROP INDEX IF EXISTS idx_sync_clients_user_id;
DROP INDEX IF EXISTS idx_sync_clients_last_seen_at;

DROP TABLE IF EXISTS sync_clients;
