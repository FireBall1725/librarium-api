-- SPDX-License-Identifier: AGPL-3.0-only
-- Add structured sort key and corporate flag to contributors.
-- sort_name stores a library-convention sort form ("Gaiman, Neil"); the
-- application backfills it on startup for existing rows. is_corporate marks
-- publishers and other non-person entities so the UI can skip name-inversion.

ALTER TABLE contributors
    ADD COLUMN sort_name    TEXT    NOT NULL DEFAULT '',
    ADD COLUMN is_corporate BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX idx_contributors_sort_name ON contributors (lower(sort_name));
