-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 fireball1725

CREATE TABLE loan_tags (
    loan_id UUID NOT NULL REFERENCES loans(id) ON DELETE CASCADE,
    tag_id  UUID NOT NULL REFERENCES tags(id)  ON DELETE CASCADE,
    PRIMARY KEY (loan_id, tag_id)
);
