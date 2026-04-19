-- Librarium initial schema
-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- ============================================================
-- Utility functions
-- ============================================================

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- sort_title strips leading definite/indefinite articles (MARC 21 filing rules)
-- so titles sort by their meaningful first word.
--   "The Way of the Househusband" → "Way of the Househusband"
--   "L'Assommoir"                 → "Assommoir"
CREATE OR REPLACE FUNCTION sort_title(t TEXT)
RETURNS TEXT
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
AS $$
  SELECT trim(
    regexp_replace(
      regexp_replace(
        trim(t),
        E'^(unos|unas|eine|the|les|una|une|des|los|las|gli|het|een|das|der|die|dem|den|det|ein|ett|uma|um|os|as|le|la|lo|el|un|de|na|az|yr|il|et|en|y|an|o|a)\\s+',
        '',
        'i'
      ),
      E'^l''',
      '',
      'i'
    )
  )
$$;

-- natural_sort_key pads embedded digit sequences so numeric segments sort numerically.
--   "Bleach #1:"  → "bleach #0000000001:"
--   "Bleach #19:" → "bleach #0000000019:"
CREATE OR REPLACE FUNCTION natural_sort_key(t TEXT)
RETURNS TEXT
LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE
AS $$
DECLARE
  result    TEXT := '';
  remaining TEXT := lower(sort_title(t));
  m         TEXT[];
BEGIN
  LOOP
    m := regexp_match(remaining, '^(.*?)([0-9]+)(.*)$');
    EXIT WHEN m IS NULL;
    result    := result || m[1] || lpad(m[2], 10, '0');
    remaining := m[3];
  END LOOP;
  RETURN result || remaining;
END;
$$;

-- ============================================================
-- Identity & Authentication
-- ============================================================

CREATE TABLE users (
    id                UUID         PRIMARY KEY,
    username          VARCHAR(64)  NOT NULL UNIQUE,
    display_name      VARCHAR(128) NOT NULL,
    email             VARCHAR(255) NOT NULL UNIQUE,
    avatar_url        VARCHAR(512),
    is_active         BOOLEAN      NOT NULL DEFAULT TRUE,
    is_instance_admin BOOLEAN      NOT NULL DEFAULT FALSE,
    last_login_at     TIMESTAMPTZ,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TRIGGER users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE user_identities (
    id               UUID         PRIMARY KEY,
    user_id          UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider         VARCHAR(64)  NOT NULL,
    provider_user_id VARCHAR(255) NOT NULL,
    credentials      JSONB,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_used_at     TIMESTAMPTZ,
    UNIQUE (provider, provider_user_id)
);

CREATE INDEX idx_user_identities_user_id ON user_identities(user_id);

CREATE TABLE refresh_tokens (
    id         UUID        PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ
);

CREATE INDEX idx_refresh_tokens_user_id ON refresh_tokens(user_id);

CREATE TABLE revoked_access_tokens (
    jti        UUID        PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_revoked_access_tokens_expires_at ON revoked_access_tokens(expires_at);

-- ============================================================
-- RBAC
-- ============================================================

CREATE TABLE roles (
    id          UUID        PRIMARY KEY,
    name        VARCHAR(64) NOT NULL UNIQUE,
    description TEXT,
    is_system   BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE permissions (
    id          UUID         PRIMARY KEY,
    name        VARCHAR(128) NOT NULL UNIQUE,
    description TEXT,
    resource    VARCHAR(64)  NOT NULL,
    action      VARCHAR(32)  NOT NULL
);

CREATE TABLE role_permissions (
    role_id       UUID NOT NULL REFERENCES roles(id)       ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

-- ============================================================
-- Libraries
-- ============================================================

CREATE TABLE libraries (
    id          UUID         PRIMARY KEY,
    name        VARCHAR(128) NOT NULL,
    description TEXT,
    slug        VARCHAR(128) NOT NULL UNIQUE,
    owner_id    UUID         NOT NULL REFERENCES users(id),
    is_public   BOOLEAN      NOT NULL DEFAULT FALSE,
    settings    JSONB        NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TRIGGER libraries_updated_at
    BEFORE UPDATE ON libraries
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE library_memberships (
    id         UUID        PRIMARY KEY,
    library_id UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    user_id    UUID        NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    role_id    UUID        NOT NULL REFERENCES roles(id),
    invited_by UUID        REFERENCES users(id),
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (library_id, user_id)
);

CREATE INDEX idx_library_memberships_user_id    ON library_memberships(user_id);
CREATE INDEX idx_library_memberships_library_id ON library_memberships(library_id);

-- ============================================================
-- Global reference data
-- ============================================================

CREATE TABLE media_types (
    id           UUID         PRIMARY KEY,
    name         VARCHAR(64)  NOT NULL UNIQUE,
    display_name VARCHAR(128) NOT NULL,
    description  TEXT,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE contributors (
    id           UUID         PRIMARY KEY,
    name         VARCHAR(255) NOT NULL,
    bio          TEXT,
    born_date    DATE,
    died_date    DATE,
    nationality  VARCHAR(64),
    external_ids JSONB        NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TRIGGER contributors_updated_at
    BEFORE UPDATE ON contributors
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX idx_contributors_name
    ON contributors USING GIN (to_tsvector('english', name));

CREATE TABLE contributor_works (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    contributor_id UUID        NOT NULL REFERENCES contributors(id) ON DELETE CASCADE,
    title          TEXT        NOT NULL,
    isbn_13        TEXT,
    isbn_10        TEXT,
    publish_year   INT,
    cover_url      TEXT,
    source         TEXT        NOT NULL,
    deleted_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX contributor_works_contributor_id_idx ON contributor_works(contributor_id);
CREATE INDEX contributor_works_isbn_13_idx ON contributor_works(isbn_13) WHERE isbn_13 IS NOT NULL;
CREATE INDEX contributor_works_isbn_10_idx ON contributor_works(isbn_10) WHERE isbn_10 IS NOT NULL;

CREATE TABLE genres (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    name_lower TEXT        NOT NULL GENERATED ALWAYS AS (lower(name)) STORED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (name_lower)
);

-- ============================================================
-- Series
-- ============================================================

CREATE TABLE series (
    id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id        UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name              VARCHAR(256) NOT NULL,
    description       TEXT,
    total_count       INT,
    status            VARCHAR(32)  NOT NULL DEFAULT 'ongoing',
    original_language VARCHAR(16),
    publication_year  INT,
    demographic       VARCHAR(32),
    genres            TEXT[]       NOT NULL DEFAULT '{}',
    url               TEXT,
    external_id       TEXT,
    external_source   TEXT,
    created_by        UUID         REFERENCES users(id),
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TRIGGER series_updated_at
    BEFORE UPDATE ON series
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX idx_series_library_id ON series(library_id);
CREATE INDEX idx_series_name ON series USING GIN (to_tsvector('english', name));

CREATE TABLE series_volumes (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    series_id    UUID        NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    position     FLOAT       NOT NULL,
    title        TEXT,
    release_date DATE,
    cover_url    TEXT,
    external_id  TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (series_id, position)
);

CREATE TRIGGER series_volumes_updated_at
    BEFORE UPDATE ON series_volumes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ============================================================
-- Books
-- ============================================================

CREATE TABLE books (
    id            UUID         PRIMARY KEY,
    library_id    UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    title         VARCHAR(512) NOT NULL,
    subtitle      VARCHAR(512),
    media_type_id UUID         NOT NULL REFERENCES media_types(id),
    description   TEXT,
    added_by      UUID         REFERENCES users(id),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TRIGGER books_updated_at
    BEFORE UPDATE ON books
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX idx_books_library_id    ON books(library_id);
CREATE INDEX idx_books_media_type_id ON books(media_type_id);
CREATE INDEX idx_books_fulltext
    ON books USING GIN (to_tsvector('english', title || ' ' || COALESCE(subtitle, '')));

CREATE TABLE book_series (
    book_id   UUID          NOT NULL REFERENCES books(id)  ON DELETE CASCADE,
    series_id UUID          NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    position  NUMERIC(6,1)  NOT NULL DEFAULT 1,
    PRIMARY KEY (book_id, series_id)
);

CREATE INDEX idx_book_series_series_id ON book_series(series_id);

CREATE TABLE book_contributors (
    book_id        UUID        NOT NULL REFERENCES books(id)         ON DELETE CASCADE,
    contributor_id UUID        NOT NULL REFERENCES contributors(id)  ON DELETE RESTRICT,
    role           VARCHAR(64) NOT NULL,
    display_order  INTEGER     NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, contributor_id, role)
);

CREATE INDEX idx_book_contributors_contributor_id ON book_contributors(contributor_id);

CREATE TABLE book_genres (
    book_id  UUID NOT NULL REFERENCES books(id)  ON DELETE CASCADE,
    genre_id UUID NOT NULL REFERENCES genres(id) ON DELETE CASCADE,
    PRIMARY KEY (book_id, genre_id)
);

CREATE INDEX book_genres_genre_id_idx ON book_genres(genre_id);

-- ============================================================
-- Storage & Editions
-- ============================================================

CREATE TABLE storage_locations (
    id            UUID          PRIMARY KEY,
    library_id    UUID          REFERENCES libraries(id) ON DELETE CASCADE,
    name          VARCHAR(128)  NOT NULL,
    root_path     VARCHAR(1024) NOT NULL,
    media_format  VARCHAR(32)   NOT NULL,
    path_template VARCHAR(512)  NOT NULL DEFAULT '{author}/{title}',
    created_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE TRIGGER storage_locations_updated_at
    BEFORE UPDATE ON storage_locations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX idx_storage_locations_library_id ON storage_locations(library_id);

CREATE TABLE book_editions (
    id                      UUID        PRIMARY KEY,
    book_id                 UUID        NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    format                  VARCHAR(32) NOT NULL,
    language                VARCHAR(16),
    edition_name            VARCHAR(255),
    narrator                VARCHAR(255),
    narrator_contributor_id UUID        REFERENCES contributors(id) ON DELETE SET NULL,
    publisher               VARCHAR(255),
    publish_date            DATE,
    isbn_10                 TEXT,
    isbn_13                 TEXT,
    copy_count              INTEGER     NOT NULL DEFAULT 1,
    description             TEXT,
    duration_seconds        INTEGER,
    page_count              INTEGER,
    is_primary              BOOLEAN     NOT NULL DEFAULT FALSE,
    acquired_at             DATE,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER book_editions_updated_at
    BEFORE UPDATE ON book_editions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX idx_book_editions_book_id ON book_editions(book_id);
CREATE INDEX idx_book_editions_isbn_13 ON book_editions(isbn_13);
CREATE INDEX idx_book_editions_isbn_10 ON book_editions(isbn_10);

CREATE TABLE edition_files (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    edition_id          UUID        NOT NULL REFERENCES book_editions(id) ON DELETE CASCADE,
    file_format         TEXT        NOT NULL,
    file_name           TEXT,
    file_path           TEXT        NOT NULL,
    storage_location_id UUID        REFERENCES storage_locations(id) ON DELETE SET NULL,
    file_size           BIGINT,
    display_order       INT         NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX edition_files_edition_id_idx ON edition_files(edition_id);

CREATE TABLE cover_images (
    id          UUID         PRIMARY KEY,
    entity_type VARCHAR(32)  NOT NULL,
    entity_id   UUID         NOT NULL,
    filename    VARCHAR(512) NOT NULL,
    mime_type   VARCHAR(64)  NOT NULL,
    file_size   BIGINT,
    checksum    VARCHAR(128),
    is_primary  BOOLEAN      NOT NULL DEFAULT FALSE,
    source_url  VARCHAR(512),
    created_by  UUID         REFERENCES users(id),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cover_images_entity ON cover_images(entity_type, entity_id);

-- ============================================================
-- Shelves & Tags
-- ============================================================

CREATE TABLE shelves (
    id            UUID         PRIMARY KEY,
    library_id    UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name          VARCHAR(128) NOT NULL,
    description   TEXT,
    color         VARCHAR(16),
    icon          VARCHAR(64),
    display_order INTEGER      NOT NULL DEFAULT 0,
    created_by    UUID         REFERENCES users(id),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TRIGGER shelves_updated_at
    BEFORE UPDATE ON shelves
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX idx_shelves_library_id ON shelves(library_id);

CREATE TABLE book_shelves (
    book_id  UUID        NOT NULL REFERENCES books(id)   ON DELETE CASCADE,
    shelf_id UUID        NOT NULL REFERENCES shelves(id) ON DELETE CASCADE,
    added_by UUID        REFERENCES users(id),
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (book_id, shelf_id)
);

CREATE TABLE tags (
    id         UUID        PRIMARY KEY,
    library_id UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name       VARCHAR(64) NOT NULL,
    color      VARCHAR(16),
    created_by UUID        REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (library_id, name)
);

CREATE INDEX idx_tags_library_id ON tags(library_id);

CREATE TABLE book_tags (
    book_id UUID NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    tag_id  UUID NOT NULL REFERENCES tags(id)  ON DELETE CASCADE,
    PRIMARY KEY (book_id, tag_id)
);

CREATE TABLE series_tags (
    series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    tag_id    UUID NOT NULL REFERENCES tags(id)   ON DELETE CASCADE,
    PRIMARY KEY (series_id, tag_id)
);

CREATE TABLE shelf_tags (
    shelf_id UUID NOT NULL REFERENCES shelves(id) ON DELETE CASCADE,
    tag_id   UUID NOT NULL REFERENCES tags(id)    ON DELETE CASCADE,
    PRIMARY KEY (shelf_id, tag_id)
);

CREATE TABLE member_tags (
    library_id UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    tag_id     UUID NOT NULL REFERENCES tags(id)      ON DELETE CASCADE,
    PRIMARY KEY (library_id, user_id, tag_id)
);

-- ============================================================
-- Per-user interactions
-- ============================================================

CREATE TABLE user_book_interactions (
    id              UUID        PRIMARY KEY,
    user_id         UUID        NOT NULL REFERENCES users(id)         ON DELETE CASCADE,
    book_edition_id UUID        NOT NULL REFERENCES book_editions(id) ON DELETE CASCADE,
    read_status     VARCHAR(32) NOT NULL DEFAULT 'unread',
    rating          SMALLINT    CHECK (rating BETWEEN 1 AND 10),
    notes           TEXT,
    review          TEXT,
    date_started    DATE,
    date_finished   DATE,
    progress        JSONB,
    is_favorite     BOOLEAN     NOT NULL DEFAULT FALSE,
    reread_count    INTEGER     NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, book_edition_id)
);

CREATE TRIGGER user_book_interactions_updated_at
    BEFORE UPDATE ON user_book_interactions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX idx_ubi_user_id    ON user_book_interactions(user_id);
CREATE INDEX idx_ubi_edition_id ON user_book_interactions(book_edition_id);
CREATE INDEX idx_ubi_status     ON user_book_interactions(read_status);

CREATE TABLE user_preferences (
    user_id    UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    prefs      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================
-- Loans
-- ============================================================

CREATE TABLE loans (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id  UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    book_id     UUID         NOT NULL REFERENCES books(id)     ON DELETE CASCADE,
    loaned_to   VARCHAR(256) NOT NULL,
    loaned_at   DATE         NOT NULL DEFAULT CURRENT_DATE,
    due_date    DATE,
    returned_at DATE,
    notes       TEXT,
    created_by  UUID         REFERENCES users(id),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TRIGGER loans_updated_at
    BEFORE UPDATE ON loans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX idx_loans_library_returned ON loans(library_id, returned_at);
CREATE INDEX idx_loans_book_id          ON loans(book_id);

CREATE TABLE loan_tags (
    loan_id UUID NOT NULL REFERENCES loans(id) ON DELETE CASCADE,
    tag_id  UUID NOT NULL REFERENCES tags(id)  ON DELETE CASCADE,
    PRIMARY KEY (loan_id, tag_id)
);

-- ============================================================
-- Wishlists
-- ============================================================

CREATE TABLE wishlist_items (
    id           UUID         PRIMARY KEY,
    user_id      UUID         NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    library_id   UUID         REFERENCES libraries(id)        ON DELETE SET NULL,
    book_id      UUID         REFERENCES books(id)            ON DELETE SET NULL,
    series_id    UUID         REFERENCES series(id)           ON DELETE SET NULL,
    title        VARCHAR(512) NOT NULL,
    author_name  VARCHAR(255),
    notes        TEXT,
    priority     SMALLINT     NOT NULL DEFAULT 0,
    external_ids JSONB        NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TRIGGER wishlist_items_updated_at
    BEFORE UPDATE ON wishlist_items
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX idx_wishlist_items_user_id ON wishlist_items(user_id);

-- ============================================================
-- Import jobs
-- ============================================================

CREATE TABLE import_jobs (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id     UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    created_by     UUID        NOT NULL REFERENCES users(id),
    status         TEXT        NOT NULL DEFAULT 'pending',
    total_rows     INT         NOT NULL DEFAULT 0,
    processed_rows INT         NOT NULL DEFAULT 0,
    failed_rows    INT         NOT NULL DEFAULT 0,
    skipped_rows   INT         NOT NULL DEFAULT 0,
    options        JSONB       NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX import_jobs_library_id_idx ON import_jobs(library_id);

CREATE TABLE import_job_items (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    import_job_id UUID        NOT NULL REFERENCES import_jobs(id) ON DELETE CASCADE,
    row_number    INT         NOT NULL,
    raw_data      JSONB       NOT NULL DEFAULT '{}',
    status        TEXT        NOT NULL DEFAULT 'pending',
    title         TEXT        NOT NULL DEFAULT '',
    isbn          TEXT        NOT NULL DEFAULT '',
    message       TEXT        NOT NULL DEFAULT '',
    book_id       UUID        REFERENCES books(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX import_job_items_job_id_idx ON import_job_items(import_job_id);

-- ============================================================
-- Enrichment batches
-- ============================================================

CREATE TABLE enrichment_batches (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id      UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    created_by      UUID        NOT NULL REFERENCES users(id),
    type            TEXT        NOT NULL CHECK (type IN ('metadata', 'cover')),
    force           BOOL        NOT NULL DEFAULT FALSE,
    status          TEXT        NOT NULL DEFAULT 'pending',
    book_ids        JSONB       NOT NULL DEFAULT '[]',
    total_books     INT         NOT NULL DEFAULT 0,
    processed_books INT         NOT NULL DEFAULT 0,
    failed_books    INT         NOT NULL DEFAULT 0,
    skipped_books   INT         NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX enrichment_batches_created_by_idx ON enrichment_batches(created_by);
CREATE INDEX enrichment_batches_library_id_idx ON enrichment_batches(library_id);

CREATE TABLE enrichment_batch_items (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id   UUID        NOT NULL REFERENCES enrichment_batches(id) ON DELETE CASCADE,
    book_id    UUID        REFERENCES books(id) ON DELETE SET NULL,
    book_title TEXT        NOT NULL DEFAULT '',
    status     TEXT        NOT NULL DEFAULT 'pending',
    message    TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX enrichment_batch_items_batch_id_idx ON enrichment_batch_items(batch_id);

-- ============================================================
-- Instance settings
-- ============================================================

CREATE TABLE instance_settings (
    key        TEXT        PRIMARY KEY,
    value      TEXT        NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
