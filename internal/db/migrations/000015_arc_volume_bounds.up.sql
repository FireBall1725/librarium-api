-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725
--
-- Optional volume-range bounds on each arc. Lets the UI place ghost rows
-- (volumes the user doesn't yet own) into the correct arc even when no owned
-- book in the arc gives us a neighbour signal. AI proposals already return
-- vol_start / vol_end; previously we discarded those on accept.

ALTER TABLE series_arcs
    ADD COLUMN vol_start NUMERIC(6,1),
    ADD COLUMN vol_end   NUMERIC(6,1);
