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
  -- Per-site JSON datastore. `state_version` backs the atomic set/inc/append
  -- ops and the ETag, so it must exist for the state API to work at all.
  state          JSONB,
  state_version  INTEGER NOT NULL DEFAULT 0,
  -- UNUSED. The private-pages feature was removed (it never locked anything at
  -- the edge). No code reads or writes this column; kept because dropping it is
  -- irreversible and it costs nothing.
  view_password_hash TEXT,
  created_at     TIMESTAMPTZ DEFAULT now(),
  updated_at     TIMESTAMPTZ DEFAULT now(),
  CONSTRAINT sites_user_name UNIQUE (user_id, name)
);

-- Whether a site is listed on the owner's public showcase at
-- sites.<domain>/<handle>. New sites default to 'unlisted': publishing to the
-- showcase is an explicit act, not a side effect of deploying.
--
-- This controls LISTING only. Both values are equally reachable by URL —
-- nothing on the content-serving path consults this column, and nginx serves
-- site files from disk without asking the application. 'unlisted' is not
-- privacy.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'unlisted'
  CHECK (visibility IN ('public', 'unlisted'));
-- Idempotent for databases created before the default flipped. Existing rows
-- keep whatever they already have; only new sites are affected.
ALTER TABLE sites ALTER COLUMN visibility SET DEFAULT 'unlisted';

-- Append-only per-site collections (guestbooks, RSVPs, signups). The other half
-- of the built-in backend alongside sites.state.
CREATE TABLE IF NOT EXISTS collection_items (
  id         BIGSERIAL PRIMARY KEY,
  site_id    UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  collection TEXT NOT NULL,
  data       JSONB NOT NULL,
  created_at TIMESTAMPTZ DEFAULT now()
);
-- id DESC: reads are newest-first within one site's collection.
CREATE INDEX IF NOT EXISTS idx_collection_items
  ON collection_items (site_id, collection, id DESC);

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
--
-- These two *_daily tables are the ORIGINAL, pre-classifier storage. They are no
-- longer written to — the ingester writes the *_hourly tables below — but they
-- are still READ, for any day earlier than the first classified day, and served
-- as the `unknown` traffic class. On a deployment that predates the classifier
-- they hold the only surviving record of that period, so DO NOT DROP THEM.
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

-- Per-endpoint API traffic, for the admin page. Go middleware aggregates in
-- memory and flushes here; raw caller IPs are kept for abuse tracking and
-- pruned after 30 days. Mirrors db/migrations/api-analytics.sql.
CREATE TABLE IF NOT EXISTS api_request_daily (
  day    DATE NOT NULL,
  route  TEXT NOT NULL,      -- normalized "METHOD /v1/pattern", bounded cardinality
  status SMALLINT NOT NULL,
  calls  BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (day, route, status)
);

CREATE TABLE IF NOT EXISTS api_ip_daily (
  day        DATE NOT NULL,
  ip         TEXT NOT NULL,
  calls      BIGINT NOT NULL DEFAULT 0,
  last_route TEXT NOT NULL DEFAULT '',
  last_seen  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (day, ip)
);

-- Lazily resolved geo per caller IP.
CREATE TABLE IF NOT EXISTS ip_geo (
  ip          TEXT PRIMARY KEY,
  country     TEXT NOT NULL DEFAULT '',
  city        TEXT NOT NULL DEFAULT '',
  org         TEXT NOT NULL DEFAULT '',
  resolved_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Current analytics storage: hourly buckets, split by who was asking.
-- Every read path in internal/db/analytics.go targets these two tables, so a
-- deployment without them has no working analytics at all (both endpoints 500,
-- and the dashboard renders zeros). Mirrors db/migrations/analytics-v2.sql.
--
-- class is one of:
--   'person' -- no automation signature; the audience number
--   'bot'    -- crawlers, AI scrapers, SEO tools, security scanners, HTTP libs
--   'infra'  -- own monitoring: loopback probes and uptime checkers
CREATE TABLE IF NOT EXISTS site_view_hourly (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  hour    TIMESTAMPTZ NOT NULL,   -- UTC, truncated to the hour
  class   TEXT NOT NULL,
  views   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, hour, class)
);
CREATE INDEX IF NOT EXISTS site_view_hourly_hour_idx ON site_view_hourly (hour);

-- One row per distinct visitor per (site, hour, class). The ingest salt is
-- stable, not rotated per day, so COUNT(DISTINCT ip_hash) over any window is
-- genuine unique visitors rather than a sum of per-day uniques.
-- country is the visitor's ISO-3166 alpha-2 code ('XX' = unresolved), resolved
-- at ingest from the local ip_country_ranges table. It lives here so uniques
-- per country are the same COUNT(DISTINCT ip_hash) as everywhere else.
CREATE TABLE IF NOT EXISTS site_visitor_hourly (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  hour    TIMESTAMPTZ NOT NULL,
  class   TEXT NOT NULL,
  ip_hash BYTEA NOT NULL,
  country TEXT NOT NULL DEFAULT 'XX',
  PRIMARY KEY (site_id, hour, class, ip_hash)
);
CREATE INDEX IF NOT EXISTS site_visitor_hourly_hour_idx ON site_visitor_hourly (hour);

-- Per-country daily traffic. Mirrors db/migrations/analytics-geo.sql.
--
-- Country is resolved on this box from ip_country_ranges; no visitor IP is ever
-- sent to a third party, and the raw IP is still never stored. 'XX' is the
-- unresolved sentinel — those rows are kept so country totals reconcile with
-- the site totals rather than quietly going missing.
CREATE TABLE IF NOT EXISTS site_geo_daily (
  site_id  UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day      DATE NOT NULL,   -- UTC
  country  TEXT NOT NULL,
  class    TEXT NOT NULL,
  views    BIGINT NOT NULL DEFAULT 0,
  visitors BIGINT NOT NULL DEFAULT 0,   -- distinct ip_hash for that day
  PRIMARY KEY (site_id, day, country, class)
);
CREATE INDEX IF NOT EXISTS site_geo_daily_day_idx ON site_geo_daily (day);

-- IP → country ranges, loaded by `ip-country-load` from a public dataset
-- (default: DB-IP IP-to-Country Lite, CC BY 4.0 — "IP Geolocation by DB-IP",
-- https://db-ip.com). Not vendored in the repo; a fresh install has an empty
-- table and every visitor resolves to 'XX' until the loader is run.
--
-- Non-overlapping ranges, IPv4 and IPv6 together (Postgres inet sorts all IPv4
-- below all IPv6). Keyed on start_ip: the containing range is the last one
-- starting at or below the address, which is one index descent.
CREATE TABLE IF NOT EXISTS ip_country_ranges (
  start_ip INET NOT NULL,
  end_ip   INET NOT NULL,
  country  TEXT NOT NULL,   -- ISO-3166 alpha-2
  PRIMARY KEY (start_ip)
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

-- ── Visitor sign-in ───────────────────────────────────────────────────────
-- Visitors are ordinary users rows; an earlier design gave them a separate
-- `visitors` table, which db/migrations/unify-identities.sql removed.

-- A user's linked Google/GitHub identity. Same human on both providers = two
-- rows pointing at one user.
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

-- In-flight authorization-code flows. Same pattern as auth_tokens: opaque
-- secret, short TTL, single-use. purpose 'site' is a visitor signing in to a
-- hosted site (site_id + host set); 'owner' is someone signing in here.
CREATE TABLE IF NOT EXISTS oauth_states (
  state          TEXT PRIMARY KEY,
  provider       TEXT NOT NULL,
  code_verifier  TEXT NOT NULL,
  return_to      TEXT NOT NULL,
  host           TEXT NOT NULL,
  site_id        UUID REFERENCES sites(id) ON DELETE CASCADE,
  purpose        TEXT NOT NULL DEFAULT 'site',
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at     TIMESTAMPTZ NOT NULL,
  used_at        TIMESTAMPTZ,
  CONSTRAINT oauth_states_provider_check CHECK (provider IN ('google', 'github')),
  CONSTRAINT oauth_states_purpose_check  CHECK (purpose IN ('site', 'owner'))
);
CREATE INDEX IF NOT EXISTS oauth_states_expires_idx ON oauth_states (expires_at);

-- Server-side session. Cookie value is id (32 bytes, stored raw).
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
