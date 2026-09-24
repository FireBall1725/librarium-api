-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725

-- Editors can delete a series. The seed (000002) gave library_editor create,
-- read and update on series but not delete, so an editor tidying the series
-- list got a 403. Deleting a series only removes the grouping; its books stay
-- in the library. Contributor and loan deletes stay with owners.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r
  JOIN permissions p ON p.name = 'series:delete'
 WHERE r.code = 'library_editor'
ON CONFLICT DO NOTHING;
