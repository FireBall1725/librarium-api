-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- Tombstones are left standing. A merge repointed real credits, and undoing
-- that is a decision rather than a rollback: the losing rows still exist and
-- can be revived by nulling merged_into, which is the whole reason it is a
-- column rather than a delete.
DROP TABLE IF EXISTS contributor_not_duplicates;
