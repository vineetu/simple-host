-- Simple Host schema. Apply once to a fresh Postgres before running the server.
-- There is no migrations framework; apply changes by hand. The trailing ALTERs
-- are idempotent-ish notes for upgrading an existing deployment.

CREATE TABLE users (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username   TEXT UNIQUE NOT NULL,
  api_key    TEXT UNIQUE NOT NULL,
  is_admin   BOOLEAN DEFAULT FALSE,
  handle             TEXT UNIQUE,          -- URL-safe public path id (^[a-z0-9-]{1,39}$); backfilled separately
  handle_changed_at  TIMESTAMPTZ,          -- last time handle was set/changed; NULL until first set
  created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE sites (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id        UUID REFERENCES users(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  active_version INTEGER NOT NULL DEFAULT 1,
  site_url       TEXT,
  expires_at     TIMESTAMPTZ,  -- NULL = permanent; set for ephemeral "preview" sites, swept when past
  allowed_origins TEXT,        -- comma-separated extra origins allowed to call this site's state/collections (for "backend anywhere" — e.g. a GitHub Pages page)
  custom_domain      TEXT UNIQUE,   -- one custom domain per site; globally unique
  domain_status      TEXT,          -- pending | active | error (NULL = no domain)
  domain_verified_at TIMESTAMPTZ,
  domain_last_error  TEXT,
  created_at     TIMESTAMPTZ DEFAULT now(),
  updated_at     TIMESTAMPTZ DEFAULT now(),
  CONSTRAINT sites_user_name UNIQUE (user_id, name)
);

-- Showcase visibility (added later): whether a site is listed on the owner's
-- public showcase at sites.<domain>/<handle>. Defaults to 'public' so every
-- existing site keeps its current behavior (all sites are already URL-reachable).
ALTER TABLE sites ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'public'
  CHECK (visibility IN ('public', 'unlisted'));

-- Frozen legacy per-site hostnames (e.g. mysite.simple-host.app) bound to a
-- site_id. Populated by a later backfill; not wired into request paths yet.
CREATE TABLE legacy_hostnames (
  hostname   TEXT PRIMARY KEY,
  site_id    UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  user_id    UUID REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_legacy_hostnames_site ON legacy_hostnames(site_id);

CREATE TABLE versions (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  site_id        UUID REFERENCES sites(id) ON DELETE CASCADE,
  version_number INTEGER NOT NULL,
  disk_path      TEXT NOT NULL,
  status         TEXT NOT NULL DEFAULT 'uploading',
  archive_sha256 TEXT NOT NULL DEFAULT '',
  created_at     TIMESTAMPTZ DEFAULT now(),
  UNIQUE(site_id, version_number)
);

CREATE TABLE auth_tokens (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email      TEXT NOT NULL,
  code       TEXT NOT NULL,
  link_token TEXT UNIQUE NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  used_at    TIMESTAMPTZ,
  attempts   INT DEFAULT 0,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- Per-site visitor analytics (nginx access log → ingester → aggregates).
CREATE TABLE IF NOT EXISTS site_view_daily (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day     DATE NOT NULL,
  views   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, day)
);
CREATE TABLE IF NOT EXISTS site_visitor_daily (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day     DATE NOT NULL,
  ip_hash BYTEA NOT NULL,
  PRIMARY KEY (site_id, day, ip_hash)
);
CREATE TABLE IF NOT EXISTS analytics_ingest_state (
  logfile      TEXT PRIMARY KEY,
  offset_bytes BIGINT NOT NULL DEFAULT 0,
  inode        BIGINT NOT NULL DEFAULT 0,
  updated_at   TIMESTAMPTZ DEFAULT now()
);

-- Upgrading an existing deployment created before archive_sha256 existed:
--   ALTER TABLE versions ADD COLUMN archive_sha256 TEXT NOT NULL DEFAULT '';
--   ALTER TABLE sites ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;
--   ALTER TABLE sites ADD COLUMN IF NOT EXISTS allowed_origins TEXT;
-- Custom domain (v3 path-model Phase 1a); live migration is applied separately:
--   ALTER TABLE sites ADD COLUMN IF NOT EXISTS custom_domain TEXT UNIQUE;
--   ALTER TABLE sites ADD COLUMN IF NOT EXISTS domain_status TEXT;
--   ALTER TABLE sites ADD COLUMN IF NOT EXISTS domain_verified_at TIMESTAMPTZ;
--   ALTER TABLE sites ADD COLUMN IF NOT EXISTS domain_last_error TEXT;
-- Visitor Google/GitHub sign-in (live migration: db/migrations/visitor-oauth.sql):
--   ALTER TABLE sites ADD COLUMN IF NOT EXISTS allow_anonymous_writes BOOLEAN NOT NULL DEFAULT FALSE;
-- One-account unification (live migration: db/migrations/unify-identities.sql):
--   DROP visitors + recreate visitor_sessions(user_id) + oauth_identities.

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
-- opaque secret, short TTL, single-use. `state` is sha256 hex of the
-- value sent to the provider; the raw token is never stored.
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

-- unify-identities.sql (appended). Safe to re-run. Destructive only to empty
-- visitor identity/session tables. Live apply: db/migrations/unify-identities.sql.

DROP TABLE IF EXISTS visitor_establish_tokens;
DROP TABLE IF EXISTS visitor_sessions;
DROP TABLE IF EXISTS visitors;

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

-- Server-side session. id is sha256 of the cookie token, never the token.
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

-- One-time bounce. `once` is sha256 hex of the URL value. The session
-- cookie is issued at consume time so this row holds no replayable secret.
CREATE TABLE IF NOT EXISTS visitor_establish_tokens (
  once         TEXT PRIMARY KEY,
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  site_id      UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  host         TEXT NOT NULL,
  return_to    TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL,
  used_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS visitor_establish_tokens_expires_idx
  ON visitor_establish_tokens (expires_at);

ALTER TABLE oauth_states
  ALTER COLUMN site_id DROP NOT NULL;

ALTER TABLE oauth_states
  ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'site';

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

COMMENT ON COLUMN visitor_sessions.id IS
  'SHA-256 of the session cookie token. The raw token is never stored.';
COMMENT ON COLUMN visitor_establish_tokens.once IS
  'SHA-256 (hex) of the one-time establish URL token. The raw token is never stored.';
COMMENT ON COLUMN oauth_states.state IS
  'SHA-256 (hex) of the OAuth state parameter. The raw token is never stored.';
