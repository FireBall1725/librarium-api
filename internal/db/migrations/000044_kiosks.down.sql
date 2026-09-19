DROP TABLE IF EXISTS kiosk_pin_failures;
ALTER TABLE users DROP COLUMN IF EXISTS kiosk_pin_hash;
DROP TABLE IF EXISTS kiosk_sessions;
DROP TABLE IF EXISTS kiosk_signin_codes;
DROP TABLE IF EXISTS kiosks;
