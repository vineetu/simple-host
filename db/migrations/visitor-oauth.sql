-- Visitor OAuth: identities, sessions, CSRF/PKCE state, establish tokens.
--
-- APPLY AS THE ROLE IN DB_DSN (the role that owns the existing tables), NOT as a
-- superuser. Applying as postgres leaves these tables owned by postgres and the
-- application role gets "permission denied for table oauth_states" at runtime —
-- the OAuth start route then 500s while everything else looks healthy.
-- If that happens: ALTER TABLE <each> OWNER TO <app role>;
-- Visitor Google/GitHub sign-in (additive; safe to re-run).
-- Operator escape hatch. Default FALSE. No owner API in v1; set via
-- ADMIN_API-Key endpoint or SQL.
ALTER TABLE sites
  ADD COLUMN IF NOT EXISTS allow_anonymous_writes BOOLEAN NOT NULL DEFAULT FALSE;

-- Durable visitor identity. Not a users row. Not an owner.
-- Same human on Google and GitHub => two rows. No email column.
CREATE TABLE IF NOT EXISTS visitors (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider         TEXT NOT NULL CHECK (provider IN ('google', 'github')),
  provider_user_id TEXT NOT NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_login_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (provider, provider_user_id)
);

-- In-flight authorization-code flows. Pattern-match of auth_tokens:
-- opaque secret, short TTL, single-use.
CREATE TABLE IF NOT EXISTS oauth_states (
  state          TEXT PRIMARY KEY,
  provider       TEXT NOT NULL CHECK (provider IN ('google', 'github')),
  code_verifier  TEXT NOT NULL,
  return_to      TEXT NOT NULL,
  host           TEXT NOT NULL,
  site_id        UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at     TIMESTAMPTZ NOT NULL,
  used_at        TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS oauth_states_expires_idx ON oauth_states (expires_at);

-- Server-side session. Cookie value is id (32 bytes, stored raw).
CREATE TABLE IF NOT EXISTS visitor_sessions (
  id              BYTEA PRIMARY KEY,
  visitor_id      UUID NOT NULL REFERENCES visitors(id) ON DELETE CASCADE,
  site_id         UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  host            TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at      TIMESTAMPTZ NOT NULL,
  idle_expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS visitor_sessions_expires_idx
  ON visitor_sessions (expires_at);
CREATE INDEX IF NOT EXISTS visitor_sessions_visitor_idx
  ON visitor_sessions (visitor_id);

-- One-time bounce from the apex callback onto the host that will Set-Cookie.
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
