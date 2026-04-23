-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- Unified jobs framework: one umbrella `jobs` table that every job kind
-- (import, enrichment, ai_suggestions, ...and future ones) writes status +
-- events into, a generalized `job_events` log, and a `job_schedules` table
-- keyed on kind for cron-driven recurring runs.
--
-- Motivation + design: plans/jobs-framework.md. This migration is PR 1:
-- schema + legacy backfill. Go-side rewiring lands in the same PR; web
-- rewires to the new endpoints in PR 2.

-- ── Umbrella tables ────────────────────────────────────────────────────────

CREATE TABLE job_schedules (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    kind          TEXT        NOT NULL UNIQUE,
    cron          TEXT        NOT NULL,
    enabled       BOOLEAN     NOT NULL DEFAULT TRUE,
    config        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    last_fired_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER job_schedules_updated_at
    BEFORE UPDATE ON job_schedules
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE jobs (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    kind         TEXT        NOT NULL,
    status       TEXT        NOT NULL DEFAULT 'pending',
                  -- pending | running | completed | failed | cancelled
    triggered_by TEXT        NOT NULL DEFAULT 'user',
                  -- user | admin | scheduler | api
    created_by   UUID        REFERENCES users(id),
    schedule_id  UUID        REFERENCES job_schedules(id) ON DELETE SET NULL,
    error        TEXT        NOT NULL DEFAULT '',
    progress     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER jobs_updated_at
    BEFORE UPDATE ON jobs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX idx_jobs_kind_status ON jobs(kind, status);
CREATE INDEX idx_jobs_created_at  ON jobs(created_at DESC);
CREATE INDEX idx_jobs_schedule_id ON jobs(schedule_id);

CREATE TABLE job_events (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id     UUID        NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    seq        INT         NOT NULL,
    type       TEXT        NOT NULL,
    content    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (job_id, seq)
);

CREATE INDEX idx_job_events_job_id ON job_events(job_id);

-- ── Legacy table FKs to the umbrella ───────────────────────────────────────
-- Each legacy "job-like" table gets a job_id column. Populated immediately
-- below. The new umbrella row is authoritative for status/error/timestamps;
-- kind-specific counters (rows/books/tokens) stay on the kind table.

ALTER TABLE import_jobs         ADD COLUMN job_id UUID REFERENCES jobs(id) ON DELETE CASCADE;
ALTER TABLE enrichment_batches  ADD COLUMN job_id UUID REFERENCES jobs(id) ON DELETE CASCADE;
ALTER TABLE ai_suggestion_runs  ADD COLUMN job_id UUID REFERENCES jobs(id) ON DELETE CASCADE;

-- ── Backfill: one jobs row per existing legacy row ─────────────────────────
-- import_jobs and enrichment_batches don't have a started_at — we reuse
-- created_at (runs start roughly when they're created in this codebase).
-- finished_at is copied from updated_at when status is terminal, null
-- otherwise. error is left empty (neither legacy table recorded one).

WITH new_rows AS (
    INSERT INTO jobs (kind, status, triggered_by, created_by, started_at, finished_at, created_at, updated_at)
    SELECT
        'import',
        status,
        'user',
        created_by,
        created_at,
        CASE WHEN status IN ('completed', 'failed', 'cancelled') THEN updated_at ELSE NULL END,
        created_at,
        updated_at
    FROM import_jobs
    ORDER BY created_at
    RETURNING id, created_at
),
paired AS (
    SELECT
        ij.id AS legacy_id,
        j.id  AS job_id
    FROM (
        SELECT id, created_at, ROW_NUMBER() OVER (ORDER BY created_at) AS rn
        FROM import_jobs
    ) ij
    JOIN (
        SELECT id, created_at, ROW_NUMBER() OVER (ORDER BY created_at) AS rn
        FROM new_rows
    ) j USING (rn)
)
UPDATE import_jobs
   SET job_id = paired.job_id
  FROM paired
 WHERE import_jobs.id = paired.legacy_id;

WITH new_rows AS (
    INSERT INTO jobs (kind, status, triggered_by, created_by, started_at, finished_at, created_at, updated_at)
    SELECT
        'enrichment',
        status,
        'user',
        created_by,
        created_at,
        CASE WHEN status IN ('completed', 'failed', 'cancelled') THEN updated_at ELSE NULL END,
        created_at,
        updated_at
    FROM enrichment_batches
    ORDER BY created_at
    RETURNING id, created_at
),
paired AS (
    SELECT
        eb.id AS legacy_id,
        j.id  AS job_id
    FROM (
        SELECT id, created_at, ROW_NUMBER() OVER (ORDER BY created_at) AS rn
        FROM enrichment_batches
    ) eb
    JOIN (
        SELECT id, created_at, ROW_NUMBER() OVER (ORDER BY created_at) AS rn
        FROM new_rows
    ) j USING (rn)
)
UPDATE enrichment_batches
   SET job_id = paired.job_id
  FROM paired
 WHERE enrichment_batches.id = paired.legacy_id;

WITH new_rows AS (
    INSERT INTO jobs (kind, status, triggered_by, created_by, error, started_at, finished_at, created_at, updated_at)
    SELECT
        'ai_suggestions',
        status,
        triggered_by,
        user_id,
        COALESCE(error, ''),
        started_at,
        finished_at,
        started_at,
        COALESCE(finished_at, started_at)
    FROM ai_suggestion_runs
    ORDER BY started_at
    RETURNING id, started_at
),
paired AS (
    SELECT
        asr.id AS legacy_id,
        j.id   AS job_id
    FROM (
        SELECT id, started_at, ROW_NUMBER() OVER (ORDER BY started_at) AS rn
        FROM ai_suggestion_runs
    ) asr
    JOIN (
        SELECT id, started_at, ROW_NUMBER() OVER (ORDER BY started_at) AS rn
        FROM new_rows
    ) j USING (rn)
)
UPDATE ai_suggestion_runs
   SET job_id = paired.job_id
  FROM paired
 WHERE ai_suggestion_runs.id = paired.legacy_id;

-- Every legacy row should now have a job_id. Lock it in.
ALTER TABLE import_jobs         ALTER COLUMN job_id SET NOT NULL;
ALTER TABLE enrichment_batches  ALTER COLUMN job_id SET NOT NULL;
ALTER TABLE ai_suggestion_runs  ALTER COLUMN job_id SET NOT NULL;

CREATE INDEX idx_import_jobs_job_id        ON import_jobs(job_id);
CREATE INDEX idx_enrichment_batches_job_id ON enrichment_batches(job_id);
CREATE INDEX idx_ai_suggestion_runs_job_id ON ai_suggestion_runs(job_id);

-- ── Move ai_run_events → job_events ────────────────────────────────────────

INSERT INTO job_events (id, job_id, seq, type, content, created_at)
SELECT ev.id, asr.job_id, ev.seq, ev.type, ev.content, ev.created_at
  FROM ai_run_events ev
  JOIN ai_suggestion_runs asr ON asr.id = ev.run_id;

DROP TABLE ai_run_events;

-- ── Seed job_schedules from existing instance_settings ─────────────────────
-- The AI suggestions job has a simple `interval_minutes` config today. Any
-- value ≥ 60 that divides evenly into hours gets the cleanest cron; anything
-- else rounds to daily at midnight UTC and the admin can fine-tune via the
-- new cron editor once PR 2 ships.

DO $$
DECLARE
    cfg JSONB;
    interval_min INT;
    cron_expr TEXT;
    was_enabled BOOLEAN;
BEGIN
    SELECT value::jsonb INTO cfg
      FROM instance_settings
     WHERE key = 'job:ai-suggestions';

    IF cfg IS NULL THEN
        -- No saved config → seed a disabled daily schedule so the admin can
        -- turn it on from the UI when they're ready.
        INSERT INTO job_schedules (kind, cron, enabled)
        VALUES ('ai_suggestions', '0 3 * * *', FALSE);
        RETURN;
    END IF;

    interval_min := COALESCE((cfg->>'interval_minutes')::INT, 0);
    was_enabled  := COALESCE((cfg->>'enabled')::BOOLEAN, FALSE);

    IF interval_min <= 0 THEN
        cron_expr := '0 3 * * *'; -- daily 3 AM fallback
    ELSIF interval_min = 60 THEN
        cron_expr := '0 * * * *';
    ELSIF interval_min % 60 = 0 AND interval_min < 1440 THEN
        -- hourly multiple: every N hours
        cron_expr := FORMAT('0 */%s * * *', (interval_min / 60)::TEXT);
    ELSIF interval_min = 1440 THEN
        cron_expr := '0 0 * * *';
    ELSIF interval_min % 1440 = 0 THEN
        -- daily multiple: every N days
        cron_expr := FORMAT('0 0 */%s * *', (interval_min / 1440)::TEXT);
    ELSE
        cron_expr := '0 3 * * *'; -- awkward value, land at daily 3 AM
    END IF;

    INSERT INTO job_schedules (kind, cron, enabled, config)
    VALUES ('ai_suggestions', cron_expr, was_enabled, cfg)
    ON CONFLICT (kind) DO UPDATE
       SET cron    = EXCLUDED.cron,
           enabled = EXCLUDED.enabled,
           config  = EXCLUDED.config;
END $$;
