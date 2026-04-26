-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725
--
-- Per-batch flag controlling whether the enrichment worker calls AI to clean
-- up descriptions after the metadata-provider step. Default false; users
-- opt in per batch (the toggle in the import / bulk-enrich UI).

ALTER TABLE enrichment_batches
    ADD COLUMN use_ai_cleanup BOOLEAN NOT NULL DEFAULT FALSE;
