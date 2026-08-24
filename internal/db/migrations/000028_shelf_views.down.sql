-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- Reverses 000028. The views hold no data of their own; everything they present
-- lives in lists and list_books and stays there.

DROP INDEX IF EXISTS lists_shelf_shape_idx;
DROP VIEW IF EXISTS library_shelf_books;
DROP VIEW IF EXISTS library_shelves;
