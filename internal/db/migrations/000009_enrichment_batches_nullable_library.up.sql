-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- Allow library_id to be NULL on enrichment_batches so we can run metadata
-- enrichment against a floating book (one not yet held by any library).
-- Suggestions-as-books surfaces a "Re-enrich" button on the BookDetailPage
-- for suggestion-backed floating books; that button needs a batch to hang
-- onto but there's no library to scope it to.
--
-- library_id stays on the row when present — it records the user's request
-- context for library-scoped batches. Admin/global batches and floating-book
-- batches just leave it NULL.

ALTER TABLE enrichment_batches
    ALTER COLUMN library_id DROP NOT NULL;
