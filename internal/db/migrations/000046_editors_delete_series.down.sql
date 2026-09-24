-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725

DELETE FROM role_permissions
 WHERE role_id = (SELECT id FROM roles WHERE code = 'library_editor')
   AND permission_id = (SELECT id FROM permissions WHERE name = 'series:delete');
