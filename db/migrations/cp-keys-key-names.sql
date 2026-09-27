-- Named, individually revocable API keys (Keys panel, GET/POST/DELETE /v1/me/keys).
--
-- APPLY AS THE ROLE IN DB_DSN (the role that owns api_keys), before the new
-- binary starts (VerifySchema refuses to boot without these columns).
-- Additive and safe while the previous binary runs: existing keys get an id and
-- NULL name/last4/last_used_at, and keep working. api_keys is small (one row
-- per sign-in), so the id backfill rewrite is brief. Safe to re-run.
BEGIN;

ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS id UUID NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX IF NOT EXISTS api_keys_id_key ON api_keys (id);
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS name TEXT;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS last4 TEXT;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ;

COMMIT;
