-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- Re-require library_id on enrichment_batches. Any rows with NULL library_id
-- (floating-book re-enrich batches) can't be downgraded cleanly; drop them
-- first so the NOT NULL constraint can be added back. Restore from a
-- pre-migration dump if that loses work you wanted to keep.

DELETE FROM enrichment_batches WHERE library_id IS NULL;

ALTER TABLE enrichment_batches
    ALTER COLUMN library_id SET NOT NULL;
