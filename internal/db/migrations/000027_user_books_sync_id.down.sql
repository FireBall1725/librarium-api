-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- Reverses 000027. The id is a surrogate that only sync reads, and the natural
-- key it sits beside is untouched, so dropping it loses no reading state.

DROP INDEX IF EXISTS user_books_id_unique;
ALTER TABLE user_books DROP COLUMN IF EXISTS id;
