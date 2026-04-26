-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725
--
-- AI metadata enrichment — observability + review queue.
--
-- ai_metadata_runs is the top-level audit trail for every AI call made on
-- behalf of metadata enrichment (description cleanup, series-info inference,
-- arc seeding). Each run records the exact prompt sent + raw response so the
-- jobs UI can expand and inspect what happened. One row per AI call; not
-- batched into multi-step pipelines like ai_suggestion_runs (those use a
-- separate event log because each run does several model calls).
--
-- ai_metadata_proposals is the review queue for review-first operations
-- (series_metadata, series_arcs). A proposal points at the run that
-- generated it, holds the structured suggestion payload, and tracks
-- accept/reject lifecycle. CleanDescription auto-applies and does not
-- create a proposal.

CREATE TABLE ai_metadata_runs (
    id                 UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id         UUID          REFERENCES libraries(id) ON DELETE CASCADE,
    job_id             UUID,         -- river job id when run inside a job
    triggered_by       UUID          REFERENCES users(id) ON DELETE SET NULL,
    kind               VARCHAR(64)   NOT NULL,  -- description_clean | series_metadata | series_arcs
    target_type        VARCHAR(32)   NOT NULL,  -- book | series
    target_id          UUID          NOT NULL,
    provider_type      VARCHAR(32)   NOT NULL,
    model_id           TEXT          NOT NULL,
    status             VARCHAR(32)   NOT NULL DEFAULT 'running', -- running | completed | failed | skipped
    error              TEXT,
    tokens_in          INT           NOT NULL DEFAULT 0,
    tokens_out         INT           NOT NULL DEFAULT 0,
    estimated_cost_usd NUMERIC(10,6) NOT NULL DEFAULT 0,
    -- prompt/response stored inline so the jobs UI can render an expandable
    -- block per run without joining a separate events table.
    prompt             TEXT,
    response_text      TEXT,
    started_at         TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    finished_at        TIMESTAMPTZ
);

CREATE INDEX idx_ai_metadata_runs_target ON ai_metadata_runs(target_type, target_id, started_at DESC);
CREATE INDEX idx_ai_metadata_runs_status ON ai_metadata_runs(status);
CREATE INDEX idx_ai_metadata_runs_job_id ON ai_metadata_runs(job_id);

CREATE TABLE ai_metadata_proposals (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id  UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    run_id      UUID         REFERENCES ai_metadata_runs(id) ON DELETE SET NULL,
    target_type VARCHAR(32)  NOT NULL,  -- 'series'
    target_id   UUID         NOT NULL,
    kind        VARCHAR(64)  NOT NULL,  -- series_metadata | series_arcs
    -- Free-form structured payload — shape depends on `kind`. For
    -- series_metadata: {status, total_count, demographic, genres, description}.
    -- For series_arcs: {arcs: [{name, position, vol_start, vol_end}]}.
    payload     JSONB        NOT NULL,
    status      VARCHAR(32)  NOT NULL DEFAULT 'pending', -- pending | accepted | rejected | partially_accepted
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    applied_at  TIMESTAMPTZ,
    applied_by  UUID         REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_ai_metadata_proposals_target ON ai_metadata_proposals(target_type, target_id, status);
CREATE INDEX idx_ai_metadata_proposals_run    ON ai_metadata_proposals(run_id);
