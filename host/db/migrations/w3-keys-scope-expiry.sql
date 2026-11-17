-- w3 hosted (2026-09-27): deploy-only keys and keys that expire.
--
-- scope: 'full' (every key so far) or 'deploy' (create, update, roll back and
-- list sites, and make preview links; nothing else). Enforced by the route
-- table in internal/auth/scope.go.
-- expires_at: an optional fixed expiry chosen when a key is minted from the
-- Keys panel (NULL = none).
-- idle_from: a key unused for KEY_IDLE_EXPIRY_DAYS stops working, counted from
-- the later of idle_from and last_used_at. Existing keys get the time this
-- migration runs, so no key in use today expires on deploy; a new key gets its
-- creation time.
--
-- Apply BEFORE deploying the build that reads it (the server refuses to start
-- without it). Idempotent. Constant and now() defaults are catalog-only
-- changes on Postgres 11+ (no table rewrite).
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS scope TEXT NOT NULL DEFAULT 'full';
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS idle_from TIMESTAMPTZ NOT NULL DEFAULT now();
