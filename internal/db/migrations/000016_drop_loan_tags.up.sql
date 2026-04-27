-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725
--
-- Loans no longer carry tags. Tags are a property of the book / shelf,
-- not the loan record — borrow rows shouldn't pull from the same shared
-- pool that holds genre-style classifications. Drop the junction.
--
-- Any existing rows are silently dropped; no migration of data is
-- attempted because tagged loans were a misuse of the field.

DROP TABLE IF EXISTS loan_tags;
