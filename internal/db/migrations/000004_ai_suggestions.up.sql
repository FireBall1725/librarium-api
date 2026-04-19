-- AI-powered book suggestions
-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- Provider config and admin permissions live in instance_settings under these keys:
--   ai:provider:anthropic   JSON { api_key, model, enabled }
--   ai:provider:openai      JSON { api_key, model, enabled }
--   ai:provider:ollama      JSON { base_url, model, enabled }
--   ai:active_provider      string ("anthropic" | "openai" | "ollama")
--   ai:permissions          JSON { reading_history, ratings, favourites, full_library, taste_profile }
-- No dedicated tables needed for those; matches the metadata-provider pattern.

-- ============================================================
-- Per-user AI settings (opt-in + taste profile)
-- ============================================================

CREATE TABLE user_ai_settings (
    user_id       UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    opt_in        BOOLEAN     NOT NULL DEFAULT FALSE,
    taste_profile JSONB       NOT NULL DEFAULT '{}'::jsonb,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER user_ai_settings_updated_at
    BEFORE UPDATE ON user_ai_settings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ============================================================
-- Suggestion run history (cost accounting + audit)
-- ============================================================

CREATE TABLE ai_suggestion_runs (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    triggered_by        VARCHAR(32) NOT NULL, -- 'scheduler' | 'admin' | 'user'
    provider_type       VARCHAR(32) NOT NULL, -- 'anthropic' | 'openai' | 'ollama'
    model_id            TEXT        NOT NULL,
    status              VARCHAR(32) NOT NULL DEFAULT 'running', -- 'running' | 'completed' | 'failed'
    error               TEXT,
    tokens_in           INT         NOT NULL DEFAULT 0,
    tokens_out          INT         NOT NULL DEFAULT 0,
    estimated_cost_usd  NUMERIC(10,6) NOT NULL DEFAULT 0,
    started_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at         TIMESTAMPTZ
);

CREATE INDEX idx_ai_runs_user_started ON ai_suggestion_runs(user_id, started_at DESC);
CREATE INDEX idx_ai_runs_status       ON ai_suggestion_runs(status);

-- ============================================================
-- Generated suggestions (replaced each run)
-- ============================================================

CREATE TABLE ai_suggestions (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    run_id          UUID         NOT NULL REFERENCES ai_suggestion_runs(id) ON DELETE CASCADE,
    type            VARCHAR(16)  NOT NULL, -- 'buy' | 'read_next'
    -- For read_next suggestions and buy suggestions that happen to already exist in library:
    book_id         UUID         REFERENCES books(id) ON DELETE SET NULL,
    book_edition_id UUID         REFERENCES book_editions(id) ON DELETE SET NULL,
    -- Denormalized metadata; always populated for display:
    title           VARCHAR(512) NOT NULL,
    author          VARCHAR(255),
    isbn            VARCHAR(20),
    cover_url       TEXT,
    reasoning       TEXT,
    status          VARCHAR(32)  NOT NULL DEFAULT 'new', -- 'new' | 'dismissed' | 'interested' | 'added_to_library'
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CHECK (type IN ('buy', 'read_next'))
);

CREATE INDEX idx_ai_suggestions_user_type_created
    ON ai_suggestions(user_id, type, created_at DESC);
CREATE INDEX idx_ai_suggestions_run_id   ON ai_suggestions(run_id);
CREATE INDEX idx_ai_suggestions_status   ON ai_suggestions(status);

-- ============================================================
-- Hard blocks ("never suggest this again") — persist across runs
-- ============================================================

CREATE TABLE ai_blocked_items (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope       VARCHAR(16)  NOT NULL, -- 'book' | 'author' | 'series'
    -- Populated according to scope:
    title       VARCHAR(512),          -- scope='book'
    author      VARCHAR(255),          -- scope='book' | 'author'
    isbn        VARCHAR(20),           -- scope='book' when known
    series_id   UUID         REFERENCES series(id) ON DELETE SET NULL, -- scope='series' when in-library
    series_name VARCHAR(256),          -- scope='series' fallback when not in-library
    blocked_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CHECK (scope IN ('book', 'author', 'series')),
    CHECK (
        (scope = 'book'   AND title IS NOT NULL) OR
        (scope = 'author' AND author IS NOT NULL) OR
        (scope = 'series' AND (series_id IS NOT NULL OR series_name IS NOT NULL))
    )
);

CREATE INDEX idx_ai_blocked_user_scope ON ai_blocked_items(user_id, scope);
