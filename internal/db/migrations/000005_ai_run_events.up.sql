-- AI suggestion run events: step-by-step observability for a single run
-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- Each row is one observable event in the pipeline: the prompt we sent,
-- the AI's response, per-candidate enrichment decisions, backfill passes,
-- read_next matches. Events are append-only; they let admins and users see
-- what actually happened inside a run (including any metadata provider
-- lookups triggered during enrichment).
CREATE TABLE ai_run_events (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id     UUID        NOT NULL REFERENCES ai_suggestion_runs(id) ON DELETE CASCADE,
    seq        INT         NOT NULL,
    -- Event type. Kept as free-form TEXT (not an enum) so the service can
    -- introduce new kinds without a schema migration. Current values:
    --   pipeline_start       — run kicked off
    --   prompt               — pass-1 prompt built and about to be sent
    --   ai_response          — pass-1 model reply (text + token counts)
    --   enrichment_decision  — one buy candidate's accept/reject outcome
    --   read_next_match      — one read_next candidate's library match result
    --   backfill_prompt      — backfill prompt (pass N)
    --   backfill_response    — backfill model reply
    --   pipeline_end         — run finishing (summary counts)
    --   error                — non-fatal diagnostic
    type       TEXT        NOT NULL,
    content    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ai_run_events_run_seq ON ai_run_events(run_id, seq);
