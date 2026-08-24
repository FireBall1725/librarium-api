-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- Reverses 000025.
--
-- This one genuinely reverses, which is only true because the up was additive:
-- it created the new shape beside the old one and dropped nothing. Every old
-- table still holds the data it held before, so removing the new tables loses
-- only what was written through them after the upgrade.
--
-- That last part is the honest caveat. Reading state written to user_books
-- since the upgrade is gone when user_books is gone, because there is no
-- dual-write keeping the old table current. Down is for backing out of a
-- migration that has just been applied, not for undoing a week of use. Restore
-- a backup for that.
--
-- The contract migration that eventually drops the old tables will NOT be
-- reversible, and its own down will say so rather than pretending.

DROP VIEW IF EXISTS effective_read_status;
DROP VIEW IF EXISTS visible_books;
DROP VIEW IF EXISTS user_permissions;

DROP TABLE IF EXISTS list_books;
DROP TABLE IF EXISTS lists;
DROP TABLE IF EXISTS wishlist;
DROP TABLE IF EXISTS reading_sessions;
DROP TABLE IF EXISTS user_books;

-- loans.copy_id has to go before copies, and copy_photos references copies too.
DROP INDEX IF EXISTS loans_copy_idx;
ALTER TABLE loans DROP COLUMN IF EXISTS copy_id;

DROP TABLE IF EXISTS copy_photos;
DROP TABLE IF EXISTS copies;
DROP TABLE IF EXISTS copy_locations;

DROP TABLE IF EXISTS edition_contributors;
DROP TABLE IF EXISTS book_contents;
DROP TABLE IF EXISTS edition_identifiers;

DROP TABLE IF EXISTS catalogue_edits;
DROP TABLE IF EXISTS user_roles;

-- auth_providers is referenced by nothing that survives, but roles is, so drop
-- the provider table before touching roles.
DROP TABLE IF EXISTS auth_providers;

-- ── Columns added to existing tables ───────────────────────────────────────

DROP INDEX IF EXISTS cover_images_edition_idx;
ALTER TABLE cover_images DROP COLUMN IF EXISTS edition_id;

ALTER TABLE series_volumes DROP COLUMN IF EXISTS external_ids;

ALTER TABLE series DROP CONSTRAINT IF EXISTS series_not_merged_into_self;
ALTER TABLE series DROP COLUMN IF EXISTS merged_into;
ALTER TABLE series DROP COLUMN IF EXISTS external_ids;

DROP INDEX IF EXISTS contributors_merged_into_idx;
ALTER TABLE contributors DROP CONSTRAINT IF EXISTS contributors_not_merged_into_self;
ALTER TABLE contributors DROP COLUMN IF EXISTS merged_into;

DROP INDEX IF EXISTS book_editions_order_idx;
ALTER TABLE book_editions DROP CONSTRAINT IF EXISTS book_editions_book_unique;
ALTER TABLE book_editions DROP CONSTRAINT IF EXISTS editions_language_bcp47;
ALTER TABLE book_editions DROP CONSTRAINT IF EXISTS editions_precision_valid;
ALTER TABLE book_editions DROP CONSTRAINT IF EXISTS editions_precision_needs_date;
ALTER TABLE book_editions DROP COLUMN IF EXISTS updated_by;
ALTER TABLE book_editions DROP COLUMN IF EXISTS external_ids;
ALTER TABLE book_editions DROP COLUMN IF EXISTS user_edited;
ALTER TABLE book_editions DROP COLUMN IF EXISTS position;
ALTER TABLE book_editions DROP COLUMN IF EXISTS publish_date_precision;
ALTER TABLE book_editions DROP COLUMN IF EXISTS description_override;
ALTER TABLE book_editions DROP COLUMN IF EXISTS subtitle_override;
ALTER TABLE book_editions DROP COLUMN IF EXISTS title_override;
-- Note: language codes normalised from eng to en are NOT restored. The mapping
-- is lossy in the direction that matters (several three-letter codes map to one
-- two-letter code), and en is correct either way.

DROP INDEX IF EXISTS books_merged_into_idx;
DROP INDEX IF EXISTS books_external_ids_idx;
DROP INDEX IF EXISTS books_title_key_idx;
ALTER TABLE books DROP CONSTRAINT IF EXISTS books_original_language_bcp47;
ALTER TABLE books DROP CONSTRAINT IF EXISTS books_not_merged_into_self;
ALTER TABLE books DROP CONSTRAINT IF EXISTS books_precision_valid;
ALTER TABLE books DROP CONSTRAINT IF EXISTS books_precision_needs_date;
ALTER TABLE books DROP COLUMN IF EXISTS updated_by;
ALTER TABLE books DROP COLUMN IF EXISTS merged_into;
ALTER TABLE books DROP COLUMN IF EXISTS external_ids;
ALTER TABLE books DROP COLUMN IF EXISTS first_published_precision;
ALTER TABLE books DROP COLUMN IF EXISTS first_published_date;
ALTER TABLE books DROP COLUMN IF EXISTS original_language;
ALTER TABLE books DROP COLUMN IF EXISTS original_title;
ALTER TABLE books DROP COLUMN IF EXISTS title_key;
ALTER TABLE books DROP COLUMN IF EXISTS sort_title;

ALTER TABLE roles DROP CONSTRAINT IF EXISTS roles_scope_unique;
DROP INDEX IF EXISTS roles_code_unique;
ALTER TABLE roles DROP CONSTRAINT IF EXISTS roles_scope_valid;
ALTER TABLE roles DROP COLUMN IF EXISTS scope;
ALTER TABLE roles DROP COLUMN IF EXISTS code;

-- ── Vocabularies ───────────────────────────────────────────────────────────
-- Last, because the tables above reference them.

DROP TABLE IF EXISTS copy_conditions;
DROP TABLE IF EXISTS contributor_roles;
DROP TABLE IF EXISTS edition_formats;
DROP TABLE IF EXISTS identifier_schemes;
