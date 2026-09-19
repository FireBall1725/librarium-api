-- An iPad kiosk on the wall of a library, and signing a member in on it.
-- See plans/ipad-kiosk.md, changes 3 to 5. Everything here is new: existing
-- clients see no difference.

-- A kiosk authenticates with a scoped, non-expiring API token minted when an
-- admin registers it. The row holds what makes the token a kiosk.
CREATE TABLE IF NOT EXISTS kiosks (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id      UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    api_token_id    UUID        NOT NULL UNIQUE REFERENCES api_tokens(id) ON DELETE CASCADE,
    name            TEXT        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    clock_24h       BOOLEAN,    -- NULL follows the iPad's region
    allow_anonymous BOOLEAN     NOT NULL DEFAULT FALSE,
    allow_signup    BOOLEAN     NOT NULL DEFAULT FALSE,
    show_borrower   BOOLEAN     NOT NULL DEFAULT TRUE,
    idle_seconds    INT         NOT NULL DEFAULT 60 CHECK (idle_seconds BETWEEN 15 AND 600),
    created_by      UUID        REFERENCES users(id) ON DELETE SET NULL,
    last_seen_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS kiosks_library_idx ON kiosks (library_id);

-- A code the kiosk shows as a QR code for a member's phone to approve. It
-- lives 60 seconds and is claimed once.
CREATE TABLE IF NOT EXISTS kiosk_signin_codes (
    code        TEXT        PRIMARY KEY,
    kiosk_id    UUID        NOT NULL REFERENCES kiosks(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    approved_by UUID        REFERENCES users(id) ON DELETE CASCADE,
    approved_at TIMESTAMPTZ,
    claimed_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS kiosk_signin_codes_expires_idx ON kiosk_signin_codes (expires_at);

-- A member signed in on a kiosk. The session itself is an API token scoped to
-- borrowing and reading that expires after 15 minutes; this row says which
-- kiosk it belongs to so the kiosk can end it.
CREATE TABLE IF NOT EXISTS kiosk_sessions (
    api_token_id UUID        PRIMARY KEY REFERENCES api_tokens(id) ON DELETE CASCADE,
    kiosk_id     UUID        NOT NULL REFERENCES kiosks(id) ON DELETE CASCADE,
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method       TEXT        NOT NULL CHECK (method IN ('phone', 'pin')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Optional, for members who'd rather tap their name than use their phone.
-- Hashed with the same scheme as passwords.
ALTER TABLE users ADD COLUMN IF NOT EXISTS kiosk_pin_hash TEXT;

-- Wrong PINs, for the lockout: 5 in 5 minutes locks that member on that
-- kiosk until they age out.
CREATE TABLE IF NOT EXISTS kiosk_pin_failures (
    kiosk_id  UUID        NOT NULL REFERENCES kiosks(id) ON DELETE CASCADE,
    user_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS kiosk_pin_failures_idx ON kiosk_pin_failures (kiosk_id, user_id, failed_at);
