-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- library_members: who is in a library, one row per person.
--
-- 000025 replaced library_memberships with user_roles, which is a grant table:
-- it carries instance-scoped grants as well as library ones, and it has no
-- unique constraint on (user_id, library_id), because holding two roles
-- somewhere is a thing a grant table has to be able to say.
--
-- library_memberships could not say it. It was UNIQUE (library_id, user_id),
-- and every read joined it expecting exactly that. Joining user_roles in its
-- place would silently multiply rows the first time anyone held two roles: a
-- books list would show a book twice, and a member list would show a person
-- twice. That is the same cardinality trap copies set, so it gets the same
-- answer, collapsed once here rather than remembered at eighteen call sites.
--
-- Which role survives the collapse is a display question, not a permissions
-- one: permission checks read user_permissions, which is the union of every
-- grant and so loses nothing. Here the most privileged role wins, measured by
-- how many permissions it actually grants rather than by a hardcoded ranking
-- that a new role would fall outside of.
CREATE VIEW library_members AS
SELECT DISTINCT ON (ur.library_id, ur.user_id)
       ur.id,
       ur.library_id,
       ur.user_id,
       ur.role_id,
       ur.granted_by AS invited_by,
       ur.granted_at AS joined_at,
       NULL::timestamptz AS deleted_at
  FROM user_roles ur
 WHERE ur.scope = 'library'
 ORDER BY ur.library_id, ur.user_id,
          (SELECT count(*) FROM role_permissions rp WHERE rp.role_id = ur.role_id) DESC,
          ur.granted_at, ur.id;

-- deleted_at is always NULL and exists so the switch is a rename at the call
-- sites rather than a rewrite of every WHERE clause. Removal was already a hard
-- DELETE, so the predicate was redundant before this and is redundant now.

CREATE INDEX IF NOT EXISTS user_roles_library_user_idx
    ON user_roles (library_id, user_id) WHERE scope = 'library';
CREATE INDEX IF NOT EXISTS user_roles_user_idx ON user_roles (user_id);
