-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- Schema tiers: server, world, possession, opinion.
--
-- Sorts every domain table by what kind of fact it holds. True of the thing
-- itself (world), true because a library holds a copy (possession), or true
-- because a person thinks so (opinion). Above those sits the server layer,
-- which exists because a shared catalogue needs someone answerable for it.
--
-- This migration is ADDITIVE ONLY. It creates the new shape and backfills it,
-- and drops nothing: every old table is left in place and untouched. Contract
-- ships in its own release, well after this one has met real collections,
-- because a .down.sql cannot restore data that was dropped.
--
-- Order throughout: create, backfill, then constrain. Guards follow the shape
-- 000008 established — count what did not map, RAISE EXCEPTION naming the count
-- and the action being refused, then apply the constraint.
--
-- Run `librarium-api preflight` against a database before upgrading it. Every
-- refusal below has a matching check there, so a conflict is a decision made in
-- advance rather than a server that will not boot.

-- ── Vocabularies ───────────────────────────────────────────────────────────
-- A vocabulary a person extends or sees is a table, extensible by INSERT. A
-- vocabulary the code branches on stays text plus a CHECK, because adding a
-- value there needs a code change anyway and a table would turn a compile-time
-- gap into a runtime one. media_types was already this pattern.
--
-- No display_name column anywhere: this app ships en-CA and fr-FR, so a label
-- in the database is a label nobody can translate.

CREATE TABLE identifier_schemes (
    code       TEXT PRIMARY KEY,
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE
);
INSERT INTO identifier_schemes (code, sort_order) VALUES
    ('isbn13', 10), ('isbn10', 20), ('asin', 30), ('lccn', 40), ('oclc', 50),
    ('ean', 60), ('upc', 70), ('isfdb_pub', 80), ('openlibrary_edition', 90),
    ('publisher_catalog', 100);

CREATE TABLE edition_formats (
    code       TEXT PRIMARY KEY,
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE
);
INSERT INTO edition_formats (code, sort_order) VALUES
    ('paperback', 10), ('hardcover', 20), ('ebook', 30), ('audiobook', 40),
    ('comic', 50), ('box_set', 60);

CREATE TABLE contributor_roles (
    code       TEXT PRIMARY KEY,
    applies_to TEXT NOT NULL CHECK (applies_to IN ('work', 'edition', 'both')),
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE
);
INSERT INTO contributor_roles (code, applies_to, sort_order) VALUES
    ('author', 'work', 10), ('editor', 'both', 20), ('illustrator', 'both', 30),
    ('contributor', 'work', 40), ('translator', 'edition', 50),
    ('narrator', 'edition', 60), ('cover_artist', 'edition', 70);
-- applies_to is advisory. Postgres cannot express "the role on this child table
-- must be one whose applies_to is work or both" with a foreign key, so the
-- application enforces it.

CREATE TABLE copy_conditions (
    code       TEXT PRIMARY KEY,
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE
);
INSERT INTO copy_conditions (code, sort_order) VALUES
    ('new', 10), ('fine', 20), ('very_good', 30), ('good', 40),
    ('fair', 50), ('poor', 60), ('ex_library', 70);

-- Any format already in use that is not in the seeded list has to become a row,
-- or the foreign key added later would reject data that is already here.
INSERT INTO edition_formats (code, sort_order, is_active)
SELECT DISTINCT lower(trim(e.format)), 900, TRUE
  FROM book_editions e
 WHERE COALESCE(trim(e.format), '') <> ''
   AND lower(trim(e.format)) NOT IN (SELECT code FROM edition_formats)
ON CONFLICT (code) DO NOTHING;

INSERT INTO contributor_roles (code, applies_to, sort_order, is_active)
SELECT DISTINCT lower(trim(bc.role)), 'both', 900, TRUE
  FROM book_contributors bc
 WHERE COALESCE(trim(bc.role), '') <> ''
   AND lower(trim(bc.role)) NOT IN (SELECT code FROM contributor_roles)
ON CONFLICT (code) DO NOTHING;

-- ── Server layer ───────────────────────────────────────────────────────────

CREATE TABLE auth_providers (
    code            TEXT PRIMARY KEY,
    kind            TEXT NOT NULL CHECK (kind IN ('password', 'oidc', 'oauth2')),
    display_name    TEXT NOT NULL,
    config          JSONB NOT NULL DEFAULT '{}',
    is_enabled      BOOLEAN NOT NULL DEFAULT FALSE,
    auto_link_email BOOLEAN NOT NULL DEFAULT FALSE,
    jit_provision   BOOLEAN NOT NULL DEFAULT FALSE,
    default_role_id UUID REFERENCES roles(id) ON DELETE RESTRICT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO auth_providers (code, kind, display_name, is_enabled)
    VALUES ('local', 'password', 'Password', TRUE);
-- display_name is a proper noun ("Authentik", "Google"), not a translatable
-- label, which is why it sits here and "ISBN-13" does not.
--
-- auto_link_email is the security-relevant one: matching an incoming identity
-- to an existing account by email address is an account-takeover vector unless
-- the provider asserts email_verified, so it is off by default.

-- Every existing identity predates the provider table, so give them a home.
INSERT INTO auth_providers (code, kind, display_name, is_enabled)
SELECT DISTINCT lower(trim(ui.provider)), 'oidc', initcap(trim(ui.provider)), TRUE
  FROM user_identities ui
 WHERE COALESCE(trim(ui.provider), '') <> ''
   AND lower(trim(ui.provider)) NOT IN (SELECT code FROM auth_providers)
ON CONFLICT (code) DO NOTHING;

-- roles gains a code and a scope. name already holds the code-like value
-- ('instance_admin'), so code is a copy; scope says where a role may be
-- granted, which is what stops an instance role being pinned to one library.
ALTER TABLE roles ADD COLUMN IF NOT EXISTS code  TEXT;
ALTER TABLE roles ADD COLUMN IF NOT EXISTS scope TEXT;

UPDATE roles SET code = name WHERE code IS NULL;
UPDATE roles SET scope = CASE
        WHEN name LIKE 'instance%' THEN 'instance'
        ELSE 'library'
    END
 WHERE scope IS NULL;

DO $$
DECLARE missing INT;
BEGIN
    SELECT count(*) INTO missing FROM roles WHERE code IS NULL OR scope IS NULL;
    IF missing > 0 THEN
        RAISE EXCEPTION 'Backfill left % roles without a code or scope; refusing to make them NOT NULL', missing;
    END IF;
END $$;

ALTER TABLE roles ALTER COLUMN code  SET NOT NULL;
ALTER TABLE roles ALTER COLUMN scope SET NOT NULL;
ALTER TABLE roles ADD CONSTRAINT roles_scope_valid CHECK (scope IN ('instance', 'library'));
CREATE UNIQUE INDEX roles_code_unique ON roles (code);
-- The composite target that lets user_roles reject a mismatch.
ALTER TABLE roles ADD CONSTRAINT roles_scope_unique UNIQUE (id, scope);

CREATE TABLE user_roles (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id    UUID NOT NULL,
    scope      TEXT NOT NULL,
    library_id UUID REFERENCES libraries(id) ON DELETE CASCADE,
    granted_by UUID REFERENCES users(id) ON DELETE SET NULL,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT user_roles_role_scope
        FOREIGN KEY (role_id, scope) REFERENCES roles (id, scope) ON DELETE CASCADE,
    CONSTRAINT user_roles_scope_matches_library CHECK (
        (scope = 'instance' AND library_id IS NULL) OR
        (scope = 'library'  AND library_id IS NOT NULL))
);
CREATE UNIQUE INDEX user_roles_library_unique
    ON user_roles (user_id, role_id, library_id) WHERE library_id IS NOT NULL;
CREATE UNIQUE INDEX user_roles_instance_unique
    ON user_roles (user_id, role_id) WHERE library_id IS NULL;
CREATE INDEX user_roles_library_idx ON user_roles (library_id);
CREATE INDEX user_roles_user_idx    ON user_roles (user_id);
-- Replaces library_memberships. Membership IS holding a library-scoped role, so
-- the two stop being separate facts that can disagree. library_id NULL means the
-- grant applies everywhere, which is how an instance admin reaches every
-- library: expressed in data rather than as a bypass inside the middleware.

-- Backfill: every membership becomes a library-scoped grant.
INSERT INTO user_roles (user_id, role_id, scope, library_id, granted_by, granted_at)
SELECT lm.user_id, lm.role_id, 'library', lm.library_id, lm.invited_by, lm.joined_at
  FROM library_memberships lm
  JOIN roles r ON r.id = lm.role_id AND r.scope = 'library'
 WHERE lm.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- ...and every instance admin becomes an instance-scoped grant, so the boolean
-- stops being a second mechanism for the same fact.
INSERT INTO user_roles (user_id, role_id, scope, library_id, granted_at)
SELECT u.id, r.id, 'instance', NULL, u.created_at
  FROM users u
  CROSS JOIN roles r
 WHERE u.is_instance_admin
   AND r.scope = 'instance'
ON CONFLICT DO NOTHING;

DO $$
DECLARE stranded INT;
BEGIN
    SELECT count(*) INTO stranded
      FROM library_memberships lm
     WHERE lm.deleted_at IS NULL
       AND NOT EXISTS (SELECT 1 FROM user_roles ur
                        WHERE ur.user_id = lm.user_id
                          AND ur.library_id = lm.library_id);
    IF stranded > 0 THEN
        RAISE EXCEPTION
          'Backfill left % library memberships without a user_roles grant; those people would lose access',
          stranded;
    END IF;
END $$;

CREATE TABLE catalogue_edits (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    action      TEXT NOT NULL DEFAULT 'edit'
                CHECK (action IN ('edit', 'merge', 'split')),
    entity_type TEXT NOT NULL
                CHECK (entity_type IN ('book', 'edition', 'contributor', 'series')),
    entity_id   UUID NOT NULL,
    field       TEXT,
    old_value   TEXT,
    new_value   TEXT,
    other_id    UUID,
    refs_moved  INTEGER,
    edited_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    edited_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT catalogue_edits_shape CHECK (
        (action = 'edit'
             AND field IS NOT NULL AND other_id IS NULL AND refs_moved IS NULL)
        OR
        (action IN ('merge', 'split')
             AND field IS NULL AND other_id IS NOT NULL AND refs_moved IS NOT NULL))
);
CREATE INDEX catalogue_edits_entity_idx ON catalogue_edits (entity_type, entity_id, edited_at DESC);
CREATE INDEX catalogue_edits_prune_idx  ON catalogue_edits (edited_at);
-- What makes shared editing safe: the catalogue is global and mutable, so an
-- admin has to be able to see what changed and put it back. entity_id is
-- polymorphic and carries no foreign key, which is the price of one log rather
-- than four. The action column exists because entity/field/old/new cannot
-- express "1,200 references moved from A to B", which would leave the log blind
-- to the most destructive thing anyone can do here. Prune on a schedule.

-- ── World: the catalogue ───────────────────────────────────────────────────

ALTER TABLE books ADD COLUMN IF NOT EXISTS sort_title                TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN IF NOT EXISTS title_key                 TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN IF NOT EXISTS original_title            TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN IF NOT EXISTS original_language         TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN IF NOT EXISTS first_published_date      DATE;
ALTER TABLE books ADD COLUMN IF NOT EXISTS first_published_precision TEXT;
ALTER TABLE books ADD COLUMN IF NOT EXISTS external_ids              JSONB NOT NULL DEFAULT '{}';
ALTER TABLE books ADD COLUMN IF NOT EXISTS merged_into               UUID REFERENCES books(id) ON DELETE SET NULL;
ALTER TABLE books ADD COLUMN IF NOT EXISTS updated_by                UUID REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE books ADD CONSTRAINT books_precision_needs_date
    CHECK ((first_published_date IS NULL) = (first_published_precision IS NULL));
ALTER TABLE books ADD CONSTRAINT books_precision_valid
    CHECK (first_published_precision IS NULL OR first_published_precision IN ('year', 'month', 'day'));
ALTER TABLE books ADD CONSTRAINT books_not_merged_into_self CHECK (merged_into <> id);
ALTER TABLE books ADD CONSTRAINT books_original_language_bcp47
    CHECK (original_language = '' OR original_language ~ '^[a-z]{2}(-[A-Z]{2})?$');

-- title_key is a normalised title, indexed and deliberately NOT unique. It only
-- ever shortlists candidates for a human to confirm. A title cannot be the key:
-- it collides (Dune the novel and the art book) and splits (Philosopher's
-- against Sorcerer's Stone) at the same time, and titles here are mutable.
UPDATE books SET title_key = regexp_replace(lower(title), '[^a-z0-9]', '', 'g')
 WHERE title_key = '';
UPDATE books SET sort_title = regexp_replace(title, '^(the|a|an)\s+', '', 'i')
 WHERE sort_title = '';

CREATE INDEX books_title_key_idx     ON books (title_key) WHERE merged_into IS NULL;
CREATE INDEX books_external_ids_idx  ON books USING GIN (external_ids jsonb_path_ops);
CREATE INDEX books_merged_into_idx   ON books (merged_into) WHERE merged_into IS NOT NULL;

ALTER TABLE book_editions ADD COLUMN IF NOT EXISTS title_override       TEXT;
ALTER TABLE book_editions ADD COLUMN IF NOT EXISTS subtitle_override    TEXT;
ALTER TABLE book_editions ADD COLUMN IF NOT EXISTS description_override TEXT;
ALTER TABLE book_editions ADD COLUMN IF NOT EXISTS publish_date_precision TEXT;
ALTER TABLE book_editions ADD COLUMN IF NOT EXISTS position             INTEGER NOT NULL DEFAULT 0;
ALTER TABLE book_editions ADD COLUMN IF NOT EXISTS user_edited          TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE book_editions ADD COLUMN IF NOT EXISTS external_ids         JSONB NOT NULL DEFAULT '{}';
ALTER TABLE book_editions ADD COLUMN IF NOT EXISTS updated_by           UUID REFERENCES users(id) ON DELETE SET NULL;

-- position replaces is_primary, which was a sort order squeezed into a boolean:
-- NOT NULL DEFAULT FALSE with no constraint and nothing clearing a previous
-- primary, so two or none were both reachable through the API. Every "which
-- edition represents this work" read becomes
--     ORDER BY position, created_at, id
-- and the trailing id is load bearing: an import can create several editions in
-- one transaction, so created_at ties to the microsecond.
UPDATE book_editions SET position = CASE WHEN is_primary THEN 0 ELSE 1 END;

-- Dates that are really year-only. 1 January is what parseFlexDate writes when
-- it is handed "1932", so it cannot be told apart from a real January date once
-- stored. Recording year precision misreads a book genuinely published on 1
-- January, which is the right trade: claiming a January date no source gave is
-- the error being fixed, and a provider refresh can correct one row.
UPDATE book_editions
   SET publish_date_precision = CASE
        WHEN EXTRACT(MONTH FROM publish_date) = 1 AND EXTRACT(DAY FROM publish_date) = 1 THEN 'year'
        ELSE 'day' END
 WHERE publish_date IS NOT NULL AND publish_date_precision IS NULL;

ALTER TABLE book_editions ADD CONSTRAINT editions_precision_needs_date
    CHECK ((publish_date IS NULL) = (publish_date_precision IS NULL));
ALTER TABLE book_editions ADD CONSTRAINT editions_precision_valid
    CHECK (publish_date_precision IS NULL OR publish_date_precision IN ('year', 'month', 'day'));

CREATE INDEX book_editions_order_idx ON book_editions (book_id, position, created_at, id);

-- Language: en on most rows, eng on others, mixing ISO 639-1 with 639-2/B, so
-- the language facet lists English twice. Normalise, then constrain. The
-- constraint is the load-bearing part: without it the provider adapters that
-- wrote 'eng' write it again and this migration gets run twice.
UPDATE book_editions SET language = CASE lower(trim(language))
        WHEN 'eng' THEN 'en' WHEN 'jpn' THEN 'ja' WHEN 'spa' THEN 'es'
        WHEN 'fre' THEN 'fr' WHEN 'fra' THEN 'fr' WHEN 'ger' THEN 'de'
        WHEN 'deu' THEN 'de' WHEN 'ita' THEN 'it' WHEN 'por' THEN 'pt'
        WHEN 'tur' THEN 'tr' WHEN 'rus' THEN 'ru' WHEN 'kor' THEN 'ko'
        WHEN 'chi' THEN 'zh' WHEN 'zho' THEN 'zh' WHEN 'dut' THEN 'nl'
        WHEN 'nld' THEN 'nl' WHEN 'pol' THEN 'pl' WHEN 'swe' THEN 'sv'
        WHEN 'dan' THEN 'da' WHEN 'nor' THEN 'no' WHEN 'fin' THEN 'fi'
        WHEN 'ara' THEN 'ar' WHEN 'heb' THEN 'he' WHEN 'lat' THEN 'la'
        WHEN 'gre' THEN 'el' WHEN 'ell' THEN 'el' WHEN 'cze' THEN 'cs'
        WHEN 'ces' THEN 'cs' WHEN 'hun' THEN 'hu' WHEN 'ron' THEN 'ro'
        WHEN 'rum' THEN 'ro' WHEN 'ukr' THEN 'uk' WHEN 'vie' THEN 'vi'
        WHEN 'tha' THEN 'th' WHEN 'ind' THEN 'id' WHEN 'hin' THEN 'hi'
        ELSE lower(trim(language)) END
 WHERE language ~ '^[A-Za-z]{3}$';

-- Anything still not BCP 47 becomes empty rather than blocking the upgrade: an
-- unrecognised code carries no more information than no code, and refusing here
-- would strand an install over a metadata field nobody reads.
UPDATE book_editions SET language = ''
 WHERE COALESCE(language, '') <> '' AND language !~ '^[a-z]{2}(-[A-Z]{2})?$';
UPDATE book_editions SET language = '' WHERE language IS NULL;

ALTER TABLE book_editions ADD CONSTRAINT editions_language_bcp47
    CHECK (language = '' OR language ~ '^[a-z]{2}(-[A-Z]{2})?$');

-- The composite target that stops a copy or a session claiming an edition of a
-- different work.
ALTER TABLE book_editions ADD CONSTRAINT book_editions_book_unique UNIQUE (id, book_id);

CREATE TABLE edition_identifiers (
    edition_id UUID NOT NULL REFERENCES book_editions(id) ON DELETE CASCADE,
    scheme     TEXT NOT NULL REFERENCES identifier_schemes(code) ON DELETE RESTRICT,
    value      TEXT NOT NULL CHECK (value <> ''),
    PRIMARY KEY (scheme, value)
);
CREATE INDEX edition_identifiers_edition_idx ON edition_identifiers (edition_id);
-- ISBN stops being a privileged column and becomes one scheme among several.
-- The primary key is the uniqueness the dedup logic has always assumed and
-- never had: isbn_13 has a plain btree today, so two editions can claim one
-- ISBN. publisher_catalog holds an Ace D-103 or a DAW UQ1010, which for a 1950s
-- paperback is the best identifier in existence and currently has nowhere to go.

-- DISTINCT ON keeps the first claimant deterministically. preflight reports any
-- collision ahead of time; the guard below refuses if one slipped through.
INSERT INTO edition_identifiers (edition_id, scheme, value)
SELECT DISTINCT ON (e.isbn_13) e.id, 'isbn13', e.isbn_13
  FROM book_editions e
 WHERE e.isbn_13 ~ '^[0-9]{13}$'
 ORDER BY e.isbn_13, e.created_at ASC, e.id ASC
ON CONFLICT DO NOTHING;

INSERT INTO edition_identifiers (edition_id, scheme, value)
SELECT DISTINCT ON (e.isbn_10) e.id, 'isbn10', e.isbn_10
  FROM book_editions e
 WHERE e.isbn_10 ~ '^[0-9]{9}[0-9Xx]$'
 ORDER BY e.isbn_10, e.created_at ASC, e.id ASC
ON CONFLICT DO NOTHING;

DO $$
DECLARE dropped INT;
BEGIN
    SELECT count(*) INTO dropped
      FROM book_editions e
     WHERE e.isbn_13 ~ '^[0-9]{13}$'
       AND NOT EXISTS (SELECT 1 FROM edition_identifiers ei
                        WHERE ei.edition_id = e.id AND ei.scheme = 'isbn13');
    IF dropped > 0 THEN
        RAISE EXCEPTION
          'Identifiers are unique per scheme and % editions share an ISBN-13 with another; reconcile them first (run preflight)',
          dropped;
    END IF;
END $$;

CREATE TABLE book_contents (
    container_id UUID NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    contained_id UUID NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    position     NUMERIC NOT NULL DEFAULT 0,
    PRIMARY KEY (container_id, contained_id),
    CONSTRAINT book_contents_no_self CHECK (container_id <> contained_id)
);
CREATE INDEX book_contents_contained_idx ON book_contents (contained_id);
-- A manga omnibus holding volumes 1 to 3, an anthology, an Ace Double. At the
-- work level rather than the edition, so an anthology's contents cannot drift
-- between its printings. Containment means "contains exactly", never
-- "overlaps": a re-cut Perfect Edition is modelled as its own series.
--
-- Nothing to backfill; this is a capability the schema did not have. The
-- transitive cycle check belongs in the application, and it is not optional:
-- visible_books walks this graph and a cycle never terminates.

CREATE TABLE edition_contributors (
    edition_id     UUID NOT NULL REFERENCES book_editions(id) ON DELETE CASCADE,
    contributor_id UUID NOT NULL REFERENCES contributors(id) ON DELETE RESTRICT,
    role           TEXT NOT NULL REFERENCES contributor_roles(code) ON DELETE RESTRICT,
    display_order  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (edition_id, contributor_id, role)
);
CREATE INDEX edition_contributors_contributor_idx ON edition_contributors (contributor_id);
-- Credits that vary by printing: translator, illustrator, cover artist,
-- narrator. book_editions.narrator_contributor_id is one hard-coded column
-- doing this job for a single role, which is the schema asking for the table.
INSERT INTO edition_contributors (edition_id, contributor_id, role, display_order)
SELECT e.id, e.narrator_contributor_id, 'narrator', 0
  FROM book_editions e
 WHERE e.narrator_contributor_id IS NOT NULL
ON CONFLICT DO NOTHING;

ALTER TABLE contributors ADD COLUMN IF NOT EXISTS merged_into UUID REFERENCES contributors(id) ON DELETE SET NULL;
ALTER TABLE contributors ADD CONSTRAINT contributors_not_merged_into_self CHECK (merged_into <> id);
CREATE INDEX contributors_merged_into_idx ON contributors (merged_into) WHERE merged_into IS NOT NULL;

ALTER TABLE series ADD COLUMN IF NOT EXISTS external_ids JSONB NOT NULL DEFAULT '{}';
ALTER TABLE series ADD COLUMN IF NOT EXISTS merged_into  UUID REFERENCES series(id) ON DELETE SET NULL;
ALTER TABLE series ADD CONSTRAINT series_not_merged_into_self CHECK (merged_into <> id);

-- external_id and external_source were a scalar pair, so a series known to both
-- MangaUpdates and AniList could record one of them.
UPDATE series
   SET external_ids = jsonb_build_object(COALESCE(NULLIF(external_source, ''), 'unknown'), external_id)
 WHERE COALESCE(external_id, '') <> '' AND external_ids = '{}';

ALTER TABLE series_volumes ADD COLUMN IF NOT EXISTS external_ids JSONB NOT NULL DEFAULT '{}';
UPDATE series_volumes
   SET external_ids = jsonb_build_object('unknown', external_id)
 WHERE COALESCE(external_id, '') <> '' AND external_ids = '{}';

-- Canonical artwork belongs to a printing, not to the work. A photograph of
-- someone's actual object is copy_photos, in the possession layer, and is never
-- shared. Today one polymorphic table attached to the book does both, so
-- photographing a battered paperback changes the cover for everyone.
ALTER TABLE cover_images ADD COLUMN IF NOT EXISTS edition_id UUID REFERENCES book_editions(id) ON DELETE CASCADE;

UPDATE cover_images ci
   SET edition_id = (SELECT e.id FROM book_editions e
                      WHERE e.book_id = ci.entity_id
                      ORDER BY e.position, e.created_at, e.id
                      LIMIT 1)
 WHERE ci.entity_type = 'book' AND ci.edition_id IS NULL;

CREATE INDEX cover_images_edition_idx ON cover_images (edition_id) WHERE edition_id IS NOT NULL;

-- ── Possession: what a library holds ───────────────────────────────────────

-- Named copy_locations rather than locations: storage_locations already exists
-- and means filesystem paths for ebook files, which is a different thing that
-- would be impossible to tell apart at a glance.
CREATE TABLE copy_locations (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    parent_id  UUID REFERENCES copy_locations(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT copy_locations_no_self CHECK (parent_id <> id),
    CONSTRAINT copy_locations_library_unique UNIQUE (id, library_id)
);
CREATE INDEX copy_locations_library_idx ON copy_locations (library_id);

CREATE TABLE copies (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id     UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    book_id        UUID NOT NULL REFERENCES books(id) ON DELETE RESTRICT,
    edition_id     UUID,
    acquired_at    DATE,
    acquired_from  TEXT NOT NULL DEFAULT '',
    acquired_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    price_minor    BIGINT CHECK (price_minor IS NULL OR price_minor >= 0),
    price_currency CHAR(3) CHECK (price_currency IS NULL OR price_currency ~ '^[A-Z]{3}$'),
    condition      TEXT REFERENCES copy_conditions(code) ON DELETE RESTRICT,
    is_signed      BOOLEAN NOT NULL DEFAULT FALSE,
    notes          TEXT NOT NULL DEFAULT '',
    location_id    UUID,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at     TIMESTAMPTZ,
    CONSTRAINT copies_price_pair CHECK ((price_minor IS NULL) = (price_currency IS NULL)),
    CONSTRAINT copies_edition_matches_book
        FOREIGN KEY (edition_id, book_id) REFERENCES book_editions (id, book_id)
        ON DELETE SET NULL (edition_id),
    CONSTRAINT copies_location_same_library
        FOREIGN KEY (location_id, library_id) REFERENCES copy_locations (id, library_id)
        ON DELETE SET NULL (location_id)
);
CREATE INDEX copies_library_idx  ON copies (library_id)  WHERE deleted_at IS NULL;
CREATE INDEX copies_book_idx     ON copies (book_id)     WHERE deleted_at IS NULL;
CREATE INDEX copies_edition_idx  ON copies (edition_id)  WHERE deleted_at IS NULL;
CREATE INDEX copies_location_idx ON copies (location_id) WHERE deleted_at IS NULL;
-- ONE ROW PER PHYSICAL OBJECT, replacing library_books and library_book_editions.
-- There is no copy_count: a counter cannot say that one of two copies is signed
-- and the other is a reading copy. There is deliberately no status column
-- either, because a row means the object is here; something on order is a
-- wishlist row until it arrives, and a row that also means "not yet" is a row
-- meaning two things.
--
-- Two composite foreign keys, not one. The first keeps edition_id pointing at a
-- printing OF THE WORK THIS ROW CLAIMS: a plain foreign key on edition_id alone
-- lets a copy claim Dune the work with a Neuromancer edition, and Postgres
-- accepts it silently. The second keeps a location inside its own library.
-- ON DELETE SET NULL (column) is the Postgres 15+ form; the plain form would try
-- to null library_id too, which is NOT NULL.

-- Holdings that name an edition become copies of that printing. copy_count is 1
-- everywhere on real data; generate_series covers the general case.
INSERT INTO copies (library_id, book_id, edition_id, acquired_at, created_at, updated_at, deleted_at)
SELECT lbe.library_id, e.book_id, e.id, lbe.acquired_at, lbe.created_at, lbe.created_at, lbe.deleted_at
  FROM library_book_editions lbe
  JOIN book_editions e ON e.id = lbe.book_edition_id
  CROSS JOIN LATERAL generate_series(1, GREATEST(COALESCE(lbe.copy_count, 1), 1)) AS n;

-- A work held with no edition recorded is a copy with a null edition, which is
-- a supported state rather than a gap.
INSERT INTO copies (library_id, book_id, edition_id, acquired_at, acquired_by, created_at, updated_at, deleted_at)
SELECT lb.library_id, lb.book_id, NULL, NULL, lb.added_by, lb.added_at, lb.added_at, lb.deleted_at
  FROM library_books lb
 WHERE NOT EXISTS (
        SELECT 1 FROM library_book_editions lbe
          JOIN book_editions e ON e.id = lbe.book_edition_id
         WHERE lbe.library_id = lb.library_id AND e.book_id = lb.book_id);

DO $$
DECLARE missing INT;
BEGIN
    SELECT count(*) INTO missing
      FROM library_books lb
     WHERE lb.deleted_at IS NULL
       AND NOT EXISTS (SELECT 1 FROM copies c
                        WHERE c.library_id = lb.library_id AND c.book_id = lb.book_id);
    IF missing > 0 THEN
        RAISE EXCEPTION
          'Backfill left % held works with no copy row; those books would vanish from their library',
          missing;
    END IF;
END $$;

CREATE TABLE copy_photos (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    copy_id    UUID NOT NULL REFERENCES copies(id) ON DELETE CASCADE,
    filename   TEXT NOT NULL,
    mime_type  TEXT NOT NULL DEFAULT '',
    file_size  BIGINT,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX copy_photos_copy_idx ON copy_photos (copy_id, position);
-- Nothing is backfilled here on purpose: there is no way to tell which existing
-- uploads were photographs of a specific object rather than provider artwork.

-- A loan points at a copy, so it says which object left the house. Today it
-- points at the work and cannot say which printing went out.
ALTER TABLE loans ADD COLUMN IF NOT EXISTS copy_id UUID REFERENCES copies(id) ON DELETE CASCADE;

UPDATE loans l
   SET copy_id = (SELECT c.id FROM copies c
                   WHERE c.library_id = l.library_id
                     AND c.book_id = l.book_id
                     AND c.deleted_at IS NULL
                   ORDER BY c.edition_id NULLS LAST, c.created_at, c.id
                   LIMIT 1)
 WHERE l.copy_id IS NULL AND l.deleted_at IS NULL;

DO $$
DECLARE stranded INT;
BEGIN
    SELECT count(*) INTO stranded FROM loans WHERE deleted_at IS NULL AND copy_id IS NULL;
    IF stranded > 0 THEN
        RAISE EXCEPTION
          'Backfill left % active loans with no copy to point at; refusing rather than losing who has what',
          stranded;
    END IF;
END $$;

CREATE INDEX loans_copy_idx ON loans (copy_id) WHERE returned_at IS NULL AND deleted_at IS NULL;

-- ── Opinion: what a person thinks ──────────────────────────────────────────

CREATE TABLE user_books (
    user_id                 UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id                 UUID NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    read_status             TEXT NOT NULL DEFAULT 'unread'
                            CHECK (read_status IN ('unread', 'reading', 'read', 'did_not_finish')),
    rating                  INTEGER CHECK (rating IS NULL OR (rating BETWEEN 1 AND 10)),
    is_favorite             BOOLEAN NOT NULL DEFAULT FALSE,
    review                  TEXT NOT NULL DEFAULT '',
    notes                   TEXT NOT NULL DEFAULT '',
    wants                   BOOLEAN NOT NULL DEFAULT FALSE,
    read_status_updated_at  TIMESTAMPTZ,
    rating_updated_at       TIMESTAMPTZ,
    is_favorite_updated_at  TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at              TIMESTAMPTZ,
    PRIMARY KEY (user_id, book_id)
);
CREATE INDEX user_books_book_idx ON user_books (book_id) WHERE deleted_at IS NULL;
-- Keyed to the WORK, not the edition. That is the change everything else hangs
-- off: starring the paperback no longer leaves the hardcover unstarred, every
-- read path stops rolling up across editions, and a book with no edition at all
-- can carry a rating, which the current schema forbids outright. Scanning a
-- borrowed copy, rating it, and buying it later becomes expressible.

-- The collapse uses the priority bestReadStatusExpr already applies, so the
-- workaround becomes the migration. Reviews and notes are the only fields where
-- a merge can lose something, and nothing is discarded: the review and notes
-- history tables already exist and the old table stays until contract.
INSERT INTO user_books (user_id, book_id, read_status, rating, is_favorite, review, notes,
                        read_status_updated_at, rating_updated_at, is_favorite_updated_at,
                        created_at, updated_at)
SELECT i.user_id, e.book_id,
       (ARRAY_AGG(i.read_status ORDER BY CASE i.read_status
            WHEN 'read' THEN 1 WHEN 'reading' THEN 2
            WHEN 'did_not_finish' THEN 3 ELSE 4 END))[1],
       MAX(i.rating),
       BOOL_OR(i.is_favorite),
       COALESCE((ARRAY_AGG(i.review ORDER BY i.updated_at DESC)
                 FILTER (WHERE COALESCE(i.review, '') <> ''))[1], ''),
       COALESCE((ARRAY_AGG(i.notes ORDER BY i.updated_at DESC)
                 FILTER (WHERE COALESCE(i.notes, '') <> ''))[1], ''),
       MAX(i.read_status_updated_at), MAX(i.rating_updated_at), MAX(i.is_favorite_updated_at),
       MIN(i.created_at), MAX(i.updated_at)
  FROM user_book_interactions i
  JOIN book_editions e ON e.id = i.book_edition_id
 WHERE i.deleted_at IS NULL
 GROUP BY i.user_id, e.book_id
ON CONFLICT (user_id, book_id) DO NOTHING;

DO $$
DECLARE missing INT;
BEGIN
    SELECT count(*) INTO missing
      FROM (SELECT DISTINCT i.user_id, e.book_id
              FROM user_book_interactions i
              JOIN book_editions e ON e.id = i.book_edition_id
             WHERE i.deleted_at IS NULL) src
     WHERE NOT EXISTS (SELECT 1 FROM user_books ub
                        WHERE ub.user_id = src.user_id AND ub.book_id = src.book_id);
    IF missing > 0 THEN
        RAISE EXCEPTION
          'Backfill left % reading records unmapped; refusing rather than losing someone''s ratings',
          missing;
    END IF;
END $$;

CREATE TABLE reading_sessions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id        UUID NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    edition_id     UUID,
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ,
    status         TEXT NOT NULL DEFAULT 'reading'
                   CHECK (status IN ('reading', 'finished', 'abandoned')),
    progress_unit  TEXT CHECK (progress_unit IN ('page', 'percent', 'seconds')),
    progress_value NUMERIC CHECK (progress_value IS NULL OR progress_value >= 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT sessions_edition_matches_book
        FOREIGN KEY (edition_id, book_id) REFERENCES book_editions (id, book_id)
        ON DELETE SET NULL (edition_id),
    CONSTRAINT sessions_progress_pair
        CHECK ((progress_unit IS NULL) = (progress_value IS NULL)),
    CONSTRAINT sessions_finished_after_started
        CHECK (finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at),
    CONSTRAINT sessions_page_needs_edition
        CHECK (progress_unit IS DISTINCT FROM 'page' OR edition_id IS NOT NULL)
);
CREATE INDEX reading_sessions_user_book_idx ON reading_sessions (user_id, book_id, started_at DESC);
-- user_books is the verdict; this is the log. Progress is typed rather than a
-- blob so it can be compared and aggregated, and a page number is meaningless
-- without knowing which printing it counts, which the last constraint enforces.
--
-- A reread is a second row. Rereads before this point cannot be reconstructed
-- because the data was never recorded: a reread_count of 3 becomes one session
-- and a derived count of 1. A real, small loss that belongs in the release
-- notes rather than hidden.
INSERT INTO reading_sessions (user_id, book_id, edition_id, started_at, finished_at, status,
                              progress_unit, progress_value, created_at)
SELECT i.user_id, e.book_id, e.id,
       i.date_started::timestamptz, i.date_finished::timestamptz,
       CASE i.read_status
            WHEN 'read' THEN 'finished'
            WHEN 'did_not_finish' THEN 'abandoned'
            ELSE 'reading' END,
       -- progress is jsonb with no documented shape and is null on every row of
       -- every collection seen so far. Read a percent out of it when one is
       -- plainly there, and otherwise carry no progress rather than inventing a
       -- number: the session still records that the book was started.
       CASE WHEN jsonb_typeof(i.progress) = 'object'
             AND jsonb_typeof(i.progress -> 'percent') = 'number'
            THEN 'percent' END,
       CASE WHEN jsonb_typeof(i.progress) = 'object'
             AND jsonb_typeof(i.progress -> 'percent') = 'number'
            THEN (i.progress ->> 'percent')::NUMERIC END,
       i.created_at
  FROM user_book_interactions i
  JOIN book_editions e ON e.id = i.book_edition_id
 WHERE i.deleted_at IS NULL
   AND (i.date_started IS NOT NULL OR i.date_finished IS NOT NULL OR i.progress IS NOT NULL);

CREATE TABLE wishlist (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id     UUID REFERENCES books(id) ON DELETE CASCADE,
    title       TEXT,
    author_name TEXT,
    notes       TEXT NOT NULL DEFAULT '',
    priority    INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT wishlist_needs_a_subject CHECK (book_id IS NOT NULL OR title IS NOT NULL)
);
CREATE UNIQUE INDEX wishlist_one_per_book ON wishlist (user_id, book_id) WHERE book_id IS NOT NULL;
-- One wishlist, not two. A row points at a catalogue entry when there is one and
-- carries free text when there is not, so "show me my wishlist" stops being a
-- union of two shapes.
INSERT INTO wishlist (user_id, book_id, title, author_name, notes, priority, created_at)
SELECT DISTINCT ON (wi.user_id, wi.book_id)
       wi.user_id, wi.book_id,
       CASE WHEN wi.book_id IS NULL THEN COALESCE(NULLIF(wi.title, ''), 'Untitled') END,
       NULLIF(wi.author_name, ''), COALESCE(wi.notes, ''), COALESCE(wi.priority, 0), wi.created_at
  FROM wishlist_items wi
 ORDER BY wi.user_id, wi.book_id, wi.created_at;

CREATE TABLE lists (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name              TEXT NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    icon              TEXT NOT NULL DEFAULT '',
    color             TEXT NOT NULL DEFAULT '',
    kind              TEXT NOT NULL CHECK (kind IN ('manual', 'smart')),
    filter            JSONB,
    filter_version    INTEGER,
    layout            TEXT NOT NULL DEFAULT 'grid' CHECK (layout IN ('grid', 'list', 'compact')),
    display_order     INTEGER NOT NULL DEFAULT 0,
    visibility        TEXT NOT NULL DEFAULT 'private'
                      CHECK (visibility IN ('private', 'library', 'public')),
    shared_library_id UUID REFERENCES libraries(id) ON DELETE RESTRICT,
    share_token       TEXT UNIQUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT lists_smart_has_filter    CHECK ((kind = 'smart') = (filter IS NOT NULL)),
    CONSTRAINT lists_filter_is_versioned CHECK ((filter IS NULL) = (filter_version IS NULL)),
    CONSTRAINT lists_shared_needs_library
        CHECK ((visibility = 'library') = (shared_library_id IS NOT NULL)),
    CONSTRAINT lists_public_needs_token
        CHECK ((visibility = 'public') = (share_token IS NOT NULL))
);
CREATE INDEX lists_owner_idx  ON lists (owner_user_id, display_order);
CREATE INDEX lists_shared_idx ON lists (shared_library_id) WHERE visibility = 'library';

CREATE TABLE list_books (
    list_id  UUID NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
    book_id  UUID NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    position NUMERIC NOT NULL DEFAULT 0,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (list_id, book_id)
);
CREATE INDEX list_books_book_idx ON list_books (book_id);
-- Shelves and saved views merge here. They were never distinguished by
-- ownership but by how membership is decided: enumerated by hand, or computed
-- from a filter. One table with a kind covers both and gives one sharing
-- implementation rather than two.
--
-- filter_version is not decoration. A stored filter is a query language with no
-- schema; unversioned, it is silently reinterpreted when the vocabulary changes.
--
-- shared_library_id is RESTRICT rather than SET NULL on purpose: nulling it
-- would violate lists_shared_needs_library, so deleting a library has to flip
-- its shared lists back to private first. That is a decision the application
-- should make out loud rather than a cascade making it silently.

-- Shelves keep what people can see today: created_by is populated, so ownership
-- is known, and the old library scope becomes a shared visibility.
INSERT INTO lists (id, owner_user_id, name, description, icon, color, kind,
                   visibility, shared_library_id, display_order, created_at, updated_at)
SELECT s.id, s.created_by, s.name, COALESCE(s.description, ''), COALESCE(s.icon, ''),
       COALESCE(s.color, ''), 'manual', 'library', s.library_id,
       COALESCE(s.display_order, 0), s.created_at, s.updated_at
  FROM shelves s
 WHERE s.created_by IS NOT NULL;

INSERT INTO list_books (list_id, book_id, added_at)
SELECT bs.shelf_id, bs.book_id, COALESCE(bs.added_at, NOW())
  FROM book_shelves bs
  JOIN lists l ON l.id = bs.shelf_id
 WHERE bs.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- user_views is migration 000024, which is in no tag: no install has ever run
-- it, so on every database but a developer's this loop finds nothing. Folding
-- it in here means lists is the only shape that ever ships.
--
-- user_views.params is TEXT of undocumented shape, and lists.filter is jsonb.
-- filter_version exists exactly so the filter shape can change without a
-- stored filter being silently reinterpreted, so version 1 is defined as
-- {"query": "<the original params string>"}. That is lossless and leaves the
-- application free to parse it, rather than this migration guessing at a
-- structure it cannot see. No install has ever run 000024, so on every database
-- but a developer's this finds nothing.
INSERT INTO lists (owner_user_id, name, icon, kind, filter, filter_version,
                   layout, display_order, visibility, created_at, updated_at)
SELECT uv.user_id, uv.name, COALESCE(uv.icon, ''), 'smart',
       jsonb_build_object('query', COALESCE(uv.params, '')), 1,
       CASE WHEN COALESCE(uv.layout, '') IN ('grid', 'list', 'compact')
            THEN uv.layout ELSE 'grid' END,
       COALESCE(uv.display_order, 0), 'private',
       COALESCE(uv.created_at, NOW()), COALESCE(uv.updated_at, NOW())
  FROM user_views uv;

-- ── Views: one definition each ─────────────────────────────────────────────

CREATE OR REPLACE VIEW user_permissions AS
SELECT ur.user_id, ur.library_id, p.name AS permission_code
  FROM user_roles ur
  JOIN role_permissions rp ON rp.role_id = ur.role_id
  JOIN permissions p ON p.id = rp.permission_id;
-- One place to ask "may this person do this here". A row with library_id NULL
-- applies to every library.

CREATE OR REPLACE VIEW visible_books AS
WITH RECURSIVE direct AS (
        SELECT ur.user_id, c.book_id
          FROM user_roles ur
          JOIN copies c ON c.library_id = ur.library_id AND c.deleted_at IS NULL
         WHERE ur.library_id IS NOT NULL
    UNION
        SELECT ur.user_id, c.book_id
          FROM user_roles ur
          JOIN role_permissions rp ON rp.role_id = ur.role_id
          JOIN permissions p ON p.id = rp.permission_id AND p.name = 'books:read'
         CROSS JOIN copies c
         WHERE ur.library_id IS NULL AND c.deleted_at IS NULL
), reachable AS (
        SELECT user_id, book_id FROM direct
    UNION
        SELECT r.user_id, bc.contained_id
          FROM reachable r
          JOIN book_contents bc ON bc.container_id = r.book_id
)
SELECT user_id, book_id FROM reachable
UNION
SELECT user_id, book_id FROM user_books WHERE deleted_at IS NULL
UNION
SELECT user_id, book_id FROM wishlist WHERE book_id IS NOT NULL
UNION
SELECT ur.user_id, lb.book_id
  FROM lists l
  JOIN list_books lb ON lb.list_id = l.id
  JOIN user_roles ur ON ur.library_id = l.shared_library_id
 WHERE l.visibility = 'library';
-- Global storage is not global visibility. The catalogue is one table so the
-- server stores Dune once, not so everyone can see it. A work is visible only
-- if the reader can see something referencing it, so creating a library grants
-- nothing because a new library is empty.
--
-- Every read joins this instead of hand-rolling a membership join. That rule is
-- repeated at four call sites today and counting; repeated at twenty it gets
-- missed at the twenty first, and a missed one here is a disclosure rather than
-- a wrong count.
--
-- The recursive arm is what makes owning an omnibus count as owning volumes 1
-- to 3. Instance-wide access is an arm here rather than a bypass inside the
-- middleware, so "who can see this" has one answer that can be queried.

CREATE OR REPLACE VIEW effective_read_status AS
WITH RECURSIVE inherited AS (
        SELECT ub.user_id, bc.contained_id AS book_id
          FROM user_books ub
          JOIN book_contents bc ON bc.container_id = ub.book_id
         WHERE ub.read_status = 'read' AND ub.deleted_at IS NULL
    UNION
        SELECT i.user_id, bc.contained_id
          FROM inherited i
          JOIN book_contents bc ON bc.container_id = i.book_id
)
SELECT user_id, book_id, read_status, FALSE AS inherited
  FROM user_books
 WHERE deleted_at IS NULL
UNION ALL
SELECT i.user_id, i.book_id, 'read', TRUE
  FROM inherited i
 WHERE NOT EXISTS (SELECT 1 FROM user_books ub
                    WHERE ub.user_id = i.user_id AND ub.book_id = i.book_id
                      AND ub.deleted_at IS NULL);
-- Visibility recursed through containment from the start and reading state did
-- not, so owning an omnibus made volumes 1 to 3 visible while marking it read
-- left all three unread.
--
-- Only 'read' is inherited: being midway through a 3-in-1 does not make volume
-- 3 in progress, and a rating is an opinion about the thing rated. An explicit
-- row always wins, so a volume marked did_not_finish stays that way. Derived,
-- never written: four rows for one action drift the moment one is undone.
