-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- Reverse the jobs framework migration. Lossy — ai_run_events lose their
-- id/created_at precision since we rebuild from job_events via a join that
-- could drop rows for jobs whose kind isn't ai_suggestions anymore.

-- Re-create ai_run_events from the subset of job_events that belong to AI
-- suggestion runs. Anything else in job_events (import/enrichment trails
-- that never existed pre-migration but might exist post-upgrade) is lost.

CREATE TABLE ai_run_events (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id     UUID        NOT NULL REFERENCES ai_suggestion_runs(id) ON DELETE CASCADE,
    seq        INT         NOT NULL,
    type       TEXT        NOT NULL,
    content    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ai_run_events_run_seq ON ai_run_events(run_id, seq);

INSERT INTO ai_run_events (id, run_id, seq, type, content, created_at)
SELECT ev.id, asr.id, ev.seq, ev.type, ev.content, ev.created_at
  FROM job_events ev
  JOIN ai_suggestion_runs asr ON asr.job_id = ev.job_id;

-- Drop the job_id FK columns (the umbrella no longer exists to point at).
DROP INDEX IF EXISTS idx_import_jobs_job_id;
DROP INDEX IF EXISTS idx_enrichment_batches_job_id;
DROP INDEX IF EXISTS idx_ai_suggestion_runs_job_id;

ALTER TABLE import_jobs         DROP COLUMN job_id;
ALTER TABLE enrichment_batches  DROP COLUMN job_id;
ALTER TABLE ai_suggestion_runs  DROP COLUMN job_id;

DROP TABLE job_events;
DROP TABLE jobs;
DROP TABLE job_schedules;
