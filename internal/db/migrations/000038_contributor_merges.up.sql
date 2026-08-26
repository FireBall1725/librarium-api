-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- Pairs somebody has looked at and said are two different people.
--
-- Duplicate detection is an inference, not a fact. The only signal that would
-- settle it is a shared external id, and contributors have almost none: one row
-- in six hundred carries any at all, because enrichment fills external ids for
-- books and never for the people who wrote them. So every candidate is a guess
-- from the name, a human confirms each one, and the ones they reject have to
-- stay rejected.
--
-- Without this the nightly sweep offers the same rejected pair every morning
-- forever, which is how a review queue becomes something nobody opens.
--
-- No candidates table beside it on purpose. What is a duplicate is a query over
-- contributors, and a stored copy of that answer would go stale the moment
-- anyone renamed anyone. Only the human decisions are worth keeping.
CREATE TABLE IF NOT EXISTS contributor_not_duplicates (
    -- Ordered by id so a pair has one row rather than two, whichever way round
    -- the reviewer happened to see it.
    lower_id     UUID NOT NULL REFERENCES contributors(id) ON DELETE CASCADE,
    higher_id    UUID NOT NULL REFERENCES contributors(id) ON DELETE CASCADE,
    dismissed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    dismissed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (lower_id, higher_id),
    CONSTRAINT contributor_not_duplicates_ordered CHECK (lower_id < higher_id)
);

CREATE INDEX IF NOT EXISTS contributor_not_duplicates_higher_idx
    ON contributor_not_duplicates (higher_id);

-- merged_into already exists. It shipped with the schema-tiers migration and
-- nothing has ever written to it or read it, which is why this migration adds
-- no column: the decision that a merge is a tombstone rather than a delete was
-- made then, and this is the release that acts on it.
--
-- What it buys: a wrong merge is undone by nulling one column, an id held by a
-- cached client or an iOS build still resolves to a real person, and the record
-- that two spellings were the same person survives instead of vanishing.
-- book_contributors.contributor_id is ON DELETE RESTRICT, so the alternative was
-- never a clean delete anyway.
