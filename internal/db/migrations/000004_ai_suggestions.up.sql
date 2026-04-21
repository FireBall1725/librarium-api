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
--
-- status values: 'running' | 'completed' | 'failed' | 'cancelled'.
-- Kept as VARCHAR (no CHECK) so the service can introduce new run states
-- without a schema migration.
-- ============================================================

CREATE TABLE ai_suggestion_runs (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    triggered_by        VARCHAR(32) NOT NULL, -- 'scheduler' | 'admin' | 'user'
    provider_type       VARCHAR(32) NOT NULL, -- 'anthropic' | 'openai' | 'ollama'
    model_id            TEXT        NOT NULL,
    status              VARCHAR(32) NOT NULL DEFAULT 'running',
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
-- Generated suggestions
--
-- run_id is nullable with ON DELETE SET NULL so the admin "clear finished
-- runs" action (and any future auto-cleanup) doesn't wipe rows the user has
-- saved (status='interested' / 'added_to_library'). The suggestion survives
-- its originating run's deletion, trading the back-pointer for durability.
-- ============================================================

CREATE TABLE ai_suggestions (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    run_id          UUID         REFERENCES ai_suggestion_runs(id) ON DELETE SET NULL,
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
    -- clock_timestamp() rather than NOW() so rows inserted in the same
    -- transaction get distinct timestamps, preserving AI output order when the
    -- UI sorts by created_at DESC. NOW() is transaction-start time — bulk
    -- INSERTs in one tx would otherwise collide on identical timestamps.
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT clock_timestamp(),
    CHECK (type IN ('buy', 'read_next'))
);

CREATE INDEX idx_ai_suggestions_user_type_created
    ON ai_suggestions(user_id, type, created_at DESC);
CREATE INDEX idx_ai_suggestions_run_id   ON ai_suggestions(run_id);
CREATE INDEX idx_ai_suggestions_status   ON ai_suggestions(status);

-- One 'new' suggestion per (user, type, normalized title). Older runs'
-- dismissed/interested/added_to_library rows are left alone — only 'new'
-- rows are deduped because those are the only ones the user hasn't
-- explicitly acted on yet.
CREATE UNIQUE INDEX idx_ai_suggestions_unique_new
    ON ai_suggestions (user_id, type, lower(title))
    WHERE status = 'new';

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

-- ============================================================
-- Per-run pipeline events (step-by-step observability)
--
-- Each row is one observable event in the pipeline: the prompt we sent,
-- the AI's response, per-candidate enrichment decisions, backfill passes,
-- read_next matches. Events are append-only; they let admins and users see
-- what actually happened inside a run (including any metadata provider
-- lookups triggered during enrichment).
--
-- type is free-form TEXT (not an enum) so the service can introduce new
-- event kinds without a schema migration. Current values:
--   pipeline_start       — run kicked off
--   prompt               — pass-1 prompt built and about to be sent
--   ai_response          — pass-1 model reply (text + token counts)
--   enrichment_decision  — one buy candidate's accept/reject outcome
--   read_next_match      — one read_next candidate's library match result
--   backfill_prompt      — backfill prompt (pass N)
--   backfill_response    — backfill model reply
--   pipeline_end         — run finishing (summary counts)
--   error                — non-fatal diagnostic
-- ============================================================

CREATE TABLE ai_run_events (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id     UUID        NOT NULL REFERENCES ai_suggestion_runs(id) ON DELETE CASCADE,
    seq        INT         NOT NULL,
    type       TEXT        NOT NULL,
    content    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ai_run_events_run_seq ON ai_run_events(run_id, seq);
