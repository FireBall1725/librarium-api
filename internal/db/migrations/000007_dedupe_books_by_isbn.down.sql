-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

-- The dedupe migration is not reversible. Once two book rows have been
-- merged into one and all FK references repointed, there is no way to
-- reconstruct the original row identities — the losing UUIDs are gone.
-- If you need to roll back the dedupe, restore from a pre-migration dump.
--
-- This down script is intentionally a no-op so `migrate down` doesn't
-- error out on the step. 000006's down migration is the reversible piece
-- of the M2M refactor.

SELECT 1 WHERE FALSE;
