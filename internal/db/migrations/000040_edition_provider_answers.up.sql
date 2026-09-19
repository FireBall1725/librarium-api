-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725

-- Every provider's answer for an edition, kept whole.
--
-- A lookup asks every provider at once and the person picks each field from
-- the answers. Keeping the answers means a field can be switched later without
-- asking again, and a provider turned on later can be asked about one book and
-- compared with the rest. One row per edition and provider: asking a provider
-- again replaces its row and leaves the others alone.
--
-- result is the provider's normalised answer (providers.BookResult) as JSON,
-- not the raw response, so a change in one provider's API doesn't change
-- what's stored.
CREATE TABLE IF NOT EXISTS edition_provider_answers (
    edition_id UUID NOT NULL REFERENCES book_editions(id) ON DELETE CASCADE,
    provider   TEXT NOT NULL,
    -- The ISBN or UPC that was looked up.
    lookup_key TEXT NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    result     JSONB NOT NULL,
    PRIMARY KEY (edition_id, provider)
);
