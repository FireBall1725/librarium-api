-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725
--
-- Append-only history for the free-text fields on user_book_interactions
-- (notes and review). Two offline devices that edit the same note never
-- silently lose either version: both end up in the history, and the
-- client surfaces a "conflicting versions" affordance the user can
-- resolve at their leisure.
--
-- The parent row's `notes` / `review` columns continue to hold the
-- current canonical content for fast reads. The history table is the
-- audit log + the source the conflict-review UI reads from.
--
-- Backfill: for every interaction with a non-empty notes or review
-- field, insert an initial history row stamped with the row's
-- updated_at. That way the sync delta from time T returns the existing
-- text as a normal history op without needing special-case logic.

CREATE TABLE user_book_interaction_notes_history (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    interaction_id  UUID        NOT NULL REFERENCES user_book_interactions(id) ON DELETE CASCADE,
    content         TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    client_id       UUID
);

CREATE INDEX idx_ubi_notes_history_interaction
    ON user_book_interaction_notes_history(interaction_id, created_at DESC);

CREATE TABLE user_book_interaction_review_history (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    interaction_id  UUID        NOT NULL REFERENCES user_book_interactions(id) ON DELETE CASCADE,
    content         TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    client_id       UUID
);

CREATE INDEX idx_ubi_review_history_interaction
    ON user_book_interaction_review_history(interaction_id, created_at DESC);

-- Backfill existing notes content as the initial history entry.
INSERT INTO user_book_interaction_notes_history (interaction_id, content, created_at)
    SELECT id, notes, updated_at
      FROM user_book_interactions
     WHERE notes IS NOT NULL AND notes <> '';

-- Backfill existing review content as the initial history entry.
INSERT INTO user_book_interaction_review_history (interaction_id, content, created_at)
    SELECT id, review, updated_at
      FROM user_book_interactions
     WHERE review IS NOT NULL AND review <> '';
