-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

DROP INDEX IF EXISTS idx_ubi_notes_history_interaction;
DROP INDEX IF EXISTS idx_ubi_review_history_interaction;

DROP TABLE IF EXISTS user_book_interaction_notes_history;
DROP TABLE IF EXISTS user_book_interaction_review_history;
