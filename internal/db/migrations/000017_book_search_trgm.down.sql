-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

DROP INDEX IF EXISTS idx_books_title_trgm;
-- Leave the extension in place — other queries may have started using it.
