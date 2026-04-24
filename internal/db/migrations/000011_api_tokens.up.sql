-- Personal access tokens (PATs) for scripted / machine access to the API.
--
-- Each row represents one `lbrm_pat_<random>` credential minted by a user from
-- the web UI. The raw token value is shown exactly once at creation and never
-- again; we store only a sha256 hash for auth comparison plus the last four
-- chars of the raw value so the UI can disambiguate tokens in the list without
-- exposing them.
--
-- Scopes cap what a token can do, independent of the user's own effective
-- permissions. An empty scopes array means "inherit user's full permissions"
-- (classic PAT behaviour); a non-empty array is AND-intersected with the
-- user's permissions at every permission check.

CREATE TABLE api_tokens (
    id            UUID        PRIMARY KEY,
    user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          TEXT        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    token_hash    TEXT        NOT NULL UNIQUE,
    token_suffix  TEXT        NOT NULL CHECK (char_length(token_suffix) = 4),
    scopes        TEXT[]      NOT NULL DEFAULT '{}',
    last_used_at  TIMESTAMPTZ,
    expires_at    TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_api_tokens_user_id     ON api_tokens(user_id);
-- Hot path: auth middleware looks up an active token by hash. The partial
-- index keeps the tree small by excluding revoked/expired rows.
CREATE INDEX idx_api_tokens_hash_active ON api_tokens(token_hash)
    WHERE revoked_at IS NULL;
