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
  created_at     TIMESTAMPTZ DEFAULT now(),
  updated_at     TIMESTAMPTZ DEFAULT now(),
  CONSTRAINT sites_user_name UNIQUE (user_id, name)
);

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

-- Upgrading an existing deployment created before archive_sha256 existed:
--   ALTER TABLE versions ADD COLUMN archive_sha256 TEXT NOT NULL DEFAULT '';
--   ALTER TABLE sites ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;
--   ALTER TABLE sites ADD COLUMN IF NOT EXISTS allowed_origins TEXT;
