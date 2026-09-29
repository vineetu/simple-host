-- v0.7.6 (2026-09-29): a passcode on a whole site (owner decision 2026-09-29).
-- passcode_enc is the passcode sealed with AES-256-GCM under the server key
-- PASSCODE_ENC_KEY (12-byte nonce, then ciphertext and tag; the site id is the
-- associated data, so a sealed value cannot be moved to another site). NULL =
-- no passcode. passcode_generation goes up whenever the passcode changes or
-- the owner signs everyone out; an unlock cookie names the generation it was
-- made for, so a bump ends every unlock. A `passcode` marker file in the site
-- folder mirrors passcode_enc for the servers that read files from disk.
-- (sites.view_password_hash is the removed July feature's column; unrelated.)
--
-- Apply BEFORE deploying the build that reads it (the server refuses to start
-- without it). Idempotent; nullable columns and a NOT NULL column with a
-- constant default are catalog-only changes (no table rewrite).
ALTER TABLE sites ADD COLUMN IF NOT EXISTS passcode_enc BYTEA;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS passcode_set_at TIMESTAMPTZ;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS passcode_generation INTEGER NOT NULL DEFAULT 0;
