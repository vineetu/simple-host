-- Account API keys at rest: store SHA-256 (hex), never the key itself.
--
-- APPLY AS THE ROLE IN DB_DSN (the role that owns users), NOT as a superuser,
-- or the application role cannot read api_keys.
--
-- Step 1 (this file) is additive and safe while the previous binary still runs:
-- it copies every existing key's hash into api_keys, so every key people hold
-- today keeps working once the new binary (which looks keys up by hash) starts.
-- Step 2 (hash-api-keys-drop-plaintext.sql) removes the plaintext column; run it
-- only after the new binary is live and verified. Safe to re-run.
BEGIN;

CREATE TABLE IF NOT EXISTS api_keys (
  key_hash   TEXT PRIMARY KEY,
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS api_keys_user_idx ON api_keys (user_id);

-- The new binary inserts users without a key.
ALTER TABLE users ALTER COLUMN api_key DROP NOT NULL;

INSERT INTO api_keys (key_hash, user_id)
SELECT encode(sha256(convert_to(api_key, 'UTF8')), 'hex'), id
  FROM users
 WHERE api_key IS NOT NULL AND api_key <> ''
ON CONFLICT (key_hash) DO NOTHING;

COMMIT;
