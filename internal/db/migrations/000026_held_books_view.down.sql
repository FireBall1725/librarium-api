-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- Reverses 000026. The view holds no data of its own, so this loses nothing:
-- everything it presented lives in copies and stays there.

DROP INDEX IF EXISTS copies_library_book_idx;
DROP VIEW IF EXISTS held_books;
