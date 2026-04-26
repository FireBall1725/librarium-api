-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725
--
-- Series can optionally be split into named arcs (manga story arcs,
-- multi-trilogy fiction sub-series like Realm of the Elderlings, etc).
-- Books opt in via book_series.arc_id; series with no arcs render flat.

CREATE TABLE series_arcs (
    id          UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    series_id   UUID          NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    name        VARCHAR(256)  NOT NULL,
    description TEXT,
    position    NUMERIC(6,1)  NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    UNIQUE (series_id, name)
);

CREATE TRIGGER series_arcs_updated_at
    BEFORE UPDATE ON series_arcs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX idx_series_arcs_series_id ON series_arcs(series_id);

-- Books still belong to a series via book_series; arc_id is optional. When
-- an arc is deleted the books in it stay in the series, just unassigned.
ALTER TABLE book_series
    ADD COLUMN arc_id UUID REFERENCES series_arcs(id) ON DELETE SET NULL;

CREATE INDEX idx_book_series_arc_id ON book_series(arc_id);
