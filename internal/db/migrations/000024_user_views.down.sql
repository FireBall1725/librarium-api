-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)
--
-- Dropping this loses every saved view, including any the reader made after
-- upgrading. Rolling back also returns the client to its localStorage store,
-- which still holds whatever it had before the one-time import, so the
-- built-ins and anything older survive the round trip. Views created only on
-- the server do not.
DROP INDEX IF EXISTS idx_user_views_user_order;
DROP TABLE IF EXISTS user_views;
