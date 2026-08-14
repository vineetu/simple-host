-- unify-identities.sql
-- Safe to re-run. Destructive only to empty visitor identity/session tables.

-- 1. Empty identity + session tables. visitors has 0 rows; the two children
--    are empty by FK. oauth_states is NOT dropped (may hold a live 10-minute
--    flow).
DROP TABLE IF EXISTS visitor_establish_tokens;
DROP TABLE IF EXISTS visitor_sessions;
DROP TABLE IF EXISTS visitors;

-- 2. Provider account ↔ users. Durable key is (provider, provider_user_id),
--    never email. email / email_verified are the snapshot at last successful
--    identify, for audit and for the first-link decision; they are not a
--    uniqueness key.
CREATE TABLE IF NOT EXISTS oauth_identities (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider         TEXT NOT NULL CHECK (provider IN ('google', 'github')),
  provider_user_id TEXT NOT NULL,
  email            TEXT,
  email_verified   BOOLEAN NOT NULL DEFAULT FALSE,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (provider, provider_user_id)
);
CREATE INDEX IF NOT EXISTS oauth_identities_user_id_idx
  ON oauth_identities (user_id);

-- 3. Site-scoped browser sessions. Cookie value is id (32 raw bytes, hex
--    on the wire), same as today. user_id is the one principal.
CREATE TABLE IF NOT EXISTS visitor_sessions (
  id              BYTEA PRIMARY KEY,
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  site_id         UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  host            TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at      TIMESTAMPTZ NOT NULL,
  idle_expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS visitor_sessions_expires_idx
  ON visitor_sessions (expires_at);
CREATE INDEX IF NOT EXISTS visitor_sessions_user_idx
  ON visitor_sessions (user_id);

CREATE TABLE IF NOT EXISTS visitor_establish_tokens (
  once         TEXT PRIMARY KEY,
  session_id   BYTEA NOT NULL REFERENCES visitor_sessions(id) ON DELETE CASCADE,
  host         TEXT NOT NULL,
  return_to    TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL,
  used_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS visitor_establish_tokens_expires_idx
  ON visitor_establish_tokens (expires_at);

-- 4. In-flight flows now serve two entry points. purpose='site' is today's
--    hosted-page flow (site_id required). purpose='owner' is dashboard
--    sign-in (site_id NULL; callback issues a /?token= handoff, not a cookie).
ALTER TABLE oauth_states
  ALTER COLUMN site_id DROP NOT NULL;

ALTER TABLE oauth_states
  ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'site';

-- Tighten the CHECK if a previous apply already added the column without it.
ALTER TABLE oauth_states
  DROP CONSTRAINT IF EXISTS oauth_states_provider_check;
ALTER TABLE oauth_states
  ADD CONSTRAINT oauth_states_provider_check
  CHECK (provider IN ('google', 'github'));

ALTER TABLE oauth_states
  DROP CONSTRAINT IF EXISTS oauth_states_purpose_check;
ALTER TABLE oauth_states
  ADD CONSTRAINT oauth_states_purpose_check
  CHECK (purpose IN ('site', 'owner'));

ALTER TABLE oauth_states
  DROP CONSTRAINT IF EXISTS oauth_states_purpose_shape;
ALTER TABLE oauth_states
  ADD CONSTRAINT oauth_states_purpose_shape
  CHECK (
    (purpose = 'site'  AND site_id IS NOT NULL AND host <> '')
    OR
    (purpose = 'owner' AND site_id IS NULL)
  );
