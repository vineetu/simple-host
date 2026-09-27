-- Simple Host schema. Apply once to a fresh Postgres before running the server.
-- Every later change also adds a file under db/migrations/, applied to existing
-- databases by `simple-host migrate` (tracked in schema_migrations; the rule for
-- those files is in db/migrations/migrations.go). The trailing ALTERs are
-- idempotent notes from before that tool existed.

CREATE TABLE users (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username   TEXT UNIQUE NOT NULL,
  is_admin   BOOLEAN DEFAULT FALSE,
  display_name       TEXT,                 -- shown on screen; never in a URL, so it is free to change
  handle             TEXT UNIQUE,          -- URL-safe public path id (^[a-z0-9-]{1,39}$); backfilled separately
  handle_changed_at  TIMESTAMPTZ,          -- last time handle was set/changed; NULL until first set
  -- Operator suspension (cp-ops-suspend.sql): keys, connected apps and
  -- sign-in refused, every site taken down; nothing deleted. NULL = active.
  suspended_at       TIMESTAMPTZ,
  suspended_reason   TEXT,
  -- Email after each owner sign-in (w2-account-signin-email.sql); the owner
  -- turns it off in the owner app (PATCH /v1/me signin_alerts).
  signin_alerts      BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- One pending sign-in email change per account (POST /v1/me/email): the codes
-- sent to the new address and to the current one, as SHA-256 hashes; both are
-- needed (w2-account-signin-email.sql, w2-signin-email-undo.sql).
CREATE TABLE email_changes (
  user_id       UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  new_email     TEXT NOT NULL,
  code_hash     TEXT NOT NULL,
  old_code_hash TEXT,
  attempts      INT NOT NULL DEFAULT 0,
  expires_at    TIMESTAMPTZ NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The undo link in the notice to the old address after a change, as a SHA-256
-- hash; used once, or expired after 7 days (w2-signin-email-undo.sql).
CREATE TABLE email_change_undos (
  token_hash TEXT PRIMARY KEY,
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  old_email  TEXT NOT NULL,
  new_email  TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  used_at    TIMESTAMPTZ
);
CREATE INDEX email_change_undos_user_idx ON email_change_undos (user_id);

-- Sign-in alerts already sent: one per account, browser/app summary and UTC
-- day (w2-account-signin-email.sql). Pruned after a couple of days.
CREATE TABLE signin_alerts_sent (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  summary TEXT NOT NULL,
  day     DATE NOT NULL,
  PRIMARY KEY (user_id, summary, day)
);

-- Account API keys, stored only as hex SHA-256. An account can hold several
-- (each sign-in issues one); each can be revoked on its own, and rotating
-- replaces them all. name and last4 are NULL on keys issued before
-- cp-keys-key-names.sql ("Earlier key" in the Keys panel).
CREATE TABLE api_keys (
  id           UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
  key_hash     TEXT PRIMARY KEY,
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT,          -- "dashboard sign-in", "agent sign-in", "event account", or typed
  last4        TEXT,          -- last 4 characters of the key, for recognising it
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at TIMESTAMPTZ,   -- written at most every 5 minutes
  scope        TEXT NOT NULL DEFAULT 'full',  -- 'full', or 'deploy': create/update/roll back/list sites and preview links only
  expires_at   TIMESTAMPTZ,   -- optional fixed expiry, chosen when minting from the Keys panel
  idle_from    TIMESTAMPTZ NOT NULL DEFAULT now()  -- idle expiry (KEY_IDLE_EXPIRY_DAYS) counts from the later of this and last_used_at
);
CREATE INDEX api_keys_user_idx ON api_keys (user_id);

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
  domain_bound_at    TIMESTAMPTZ,
  domain_last_error  TEXT,
  previous_domain    TEXT,          -- the proven address still served while a new domain is pending
  domain_cert_status TEXT,          -- pending | issuing | live | failed (NULL = none)
  domain_failing_since     TIMESTAMPTZ, -- a verified domain first failed its checks
  domain_lapse_notified_at TIMESTAMPTZ, -- the owner was emailed about the failing domain
  previous_domain_failing_since TIMESTAMPTZ, -- the earlier address first failed its checks (let go after 72 h)
  -- Proof a custom domain is the owner's: a DNS TXT record _simple-host.<domain>
  -- whose value is this token (per site, never changes).
  domain_token TEXT DEFAULT ('sh-' || replace(gen_random_uuid()::text, '-', '')),
  -- Domains verified before the TXT proof existed (2026-09-27): they keep
  -- working without it while they stay verified; a lapse needs the proof.
  domain_proof_exempt TEXT[] NOT NULL DEFAULT '{}',
  -- Per-site JSON datastore. `state_version` backs the atomic set/inc/append
  -- ops and the ETag, so it must exist for the state API to work at all.
  state          JSONB,
  state_version  INTEGER NOT NULL DEFAULT 0,
  -- UNUSED. The private-pages feature was removed (it never locked anything at
  -- the edge). No code reads or writes this column; kept because dropping it is
  -- irreversible and it costs nothing.
  view_password_hash TEXT,
  -- Operator take-down (cp-ops-suspend.sql): the site keeps everything, is
  -- served as "taken down" on every address and refuses changes. NULL = live.
  -- A `suspended` marker file in the site folder mirrors it for the servers
  -- that read files straight from disk.
  suspended_at     TIMESTAMPTZ,
  suspended_reason TEXT,
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

-- Recently deleted (mirrors db/migrations/cp-recover-recently-deleted.sql).
-- A deleted site keeps its row, data and name for 7 days; NULL = live.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_sites_deleted_at ON sites (deleted_at) WHERE deleted_at IS NOT NULL;

-- Taken offline by its owner (mirrors db/migrations/w2-sites-offline.sql):
-- every address shows "This site is offline" and visitor saves are refused;
-- nothing is deleted. NULL = online. An `offline` marker file in the site
-- folder mirrors it for the servers that read files straight from disk.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS offline_at TIMESTAMPTZ;

-- Old names of renamed sites (mirrors db/migrations/w2-sites-old-names.sql):
-- links to an old name 302 to the site's current address until a site of
-- that name exists again. A site in Recently deleted is skipped.
CREATE TABLE IF NOT EXISTS site_name_aliases (
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  site_id    UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, name)
);
CREATE INDEX IF NOT EXISTS idx_site_name_aliases_site ON site_name_aliases (site_id);
-- Idle-site cleanup (mirrors db/migrations/w2-addr-idle-cleanup.sql).
-- idle_keep: the owner's Keep flag (never warned or removed); idle_kept_at: the
-- last "Keep it" (resets the idle clock); idle_warned_at: owner emailed that
-- the site is idle; idle_removed_at: the cleanup moved it to Recently deleted;
-- idle_token_hash: SHA-256 of the token the emailed links carry.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS idle_keep BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS idle_kept_at TIMESTAMPTZ;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS idle_warned_at TIMESTAMPTZ;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS idle_removed_at TIMESTAMPTZ;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS idle_token_hash BYTEA;
-- Promised dates (mirrors db/migrations/knobs-promised-dates.sql), stored when
-- the promise is made so a later settings change applies to new events only:
-- purge_at (deleted site removed for good), idle_remove_at (idle-warned site
-- moves to Recently deleted), domain_release_at (failing domain disconnected).
ALTER TABLE sites ADD COLUMN IF NOT EXISTS purge_at TIMESTAMPTZ;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS idle_remove_at TIMESTAMPTZ;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS domain_release_at TIMESTAMPTZ;

-- Append-only per-site collections (guestbooks, RSVPs, signups). The other half
-- of the built-in backend alongside sites.state.
CREATE TABLE IF NOT EXISTS collection_items (
  id         BIGSERIAL PRIMARY KEY,
  site_id    UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  collection TEXT NOT NULL,
  data       JSONB NOT NULL,
  created_at TIMESTAMPTZ DEFAULT now(),
  -- Who submitted an item to a PRIVATE collection (server-set; NULL otherwise).
  submitted_by UUID REFERENCES users(id) ON DELETE SET NULL
);
-- id DESC: reads are newest-first within one site's collection.
CREATE INDEX IF NOT EXISTS idx_collection_items
  ON collection_items (site_id, collection, id DESC);

-- Private collections (2026-09-24; mirrors db/migrations/private-collections.sql).
-- No row = public, the default. submitted_by is set by the server from the
-- visitor session on a private collection, never from the request body.
CREATE TABLE IF NOT EXISTS collection_settings (
  site_id    UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  collection TEXT NOT NULL,
  private    BOOLEAN NOT NULL DEFAULT false,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (site_id, collection)
);

-- Saved data, step 1 (mirrors db/migrations/sd1-saved-data-safety.sql):
-- history and undo, recoverable deletes, authors, idempotency, the watch.
-- Every site that exists before the kinds arrive keeps today's open behaviour.
-- The default stays true; the server creates every new site with false
-- (step 2, kinds: with SAVED_DATA_DEFAULT_KIND=declare_first a name nobody
-- declared takes no saves there; by default it is Shared everywhere).
ALTER TABLE sites ADD COLUMN IF NOT EXISTS legacy_data BOOLEAN NOT NULL DEFAULT true;

-- Deleted and cleared items stay for the undo window (NULL = live), and the
-- address the author wrote from is kept beside the account id.
ALTER TABLE collection_items ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE collection_items ADD COLUMN IF NOT EXISTS submitted_email TEXT;
CREATE INDEX IF NOT EXISTS idx_collection_items_deleted
  ON collection_items (site_id, collection, deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_collection_items_submitted_by
  ON collection_items (submitted_by) WHERE submitted_by IS NOT NULL;

-- One row per change: the value before it (prev), what changed, who and when.
-- kind 'state' is the site's saved-data document (name ''), kind 'list' one
-- item of a list (item_id). Kept 30 days by time, thinned past a per-site cap
-- but always keeping each item's first change of every day.
CREATE TABLE IF NOT EXISTS data_history (
  id          BIGSERIAL PRIMARY KEY,
  site_id     UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL CHECK (kind IN ('state', 'list')),
  name        TEXT NOT NULL DEFAULT '',
  item_id     BIGINT REFERENCES collection_items(id) ON DELETE CASCADE,
  op          TEXT NOT NULL,
  prev        JSONB,
  actor_id    UUID REFERENCES users(id) ON DELETE SET NULL,
  actor_kind  TEXT NOT NULL,
  actor_email TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_data_history_target ON data_history (site_id, kind, name, id DESC);
CREATE INDEX IF NOT EXISTS idx_data_history_item ON data_history (item_id) WHERE item_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_data_history_created ON data_history (created_at);
CREATE INDEX IF NOT EXISTS idx_data_history_actor ON data_history (actor_id) WHERE actor_id IS NOT NULL;

-- A write retried with the same Idempotency-Key is saved once: the first
-- answer is kept (24 hours) and replayed. scope is a hash of the key, the
-- route and the caller; status 0 = the first request is still running.
CREATE TABLE IF NOT EXISTS idempotency_keys (
  scope        BYTEA PRIMARY KEY,
  status       INTEGER NOT NULL DEFAULT 0,
  etag         TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_idempotency_keys_created ON idempotency_keys (created_at);

-- The 7-day watch: per site and day, how often each saved-data use that a
-- later step tightens happened (visitor whole-document replace, non-object
-- documents, visitor ops by type, large visitor increments, new list names,
-- large list items). Counts only, never content. Read by /v1/admin/data-watch.
CREATE TABLE IF NOT EXISTS data_watch (
  day     DATE NOT NULL,
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  metric  TEXT NOT NULL,
  count   BIGINT NOT NULL DEFAULT 0,
  last_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (day, site_id, metric)
);

-- Saved data, step 1, review fixes (mirrors
-- db/migrations/sd1-saved-data-safety2-limits.sql).

-- What a site's live saved data takes: the page-data document (state_bytes)
-- plus every live list item (deleted items and history are not counted).
-- Kept by the triggers below in the same transaction as every write, so the
-- per-site cap (SAVED_DATA_SITE_MAX_MB) is one row read, never a scan.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS state_bytes BIGINT NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS data_bytes BIGINT NOT NULL DEFAULT 0;

CREATE OR REPLACE FUNCTION sh_sites_state_bytes() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  s BIGINT := COALESCE(octet_length(NEW.state::text), 0);
BEGIN
  IF TG_OP = 'INSERT' THEN
    NEW.data_bytes := s;
  ELSE
    NEW.data_bytes := NEW.data_bytes + s - OLD.state_bytes;
  END IF;
  NEW.state_bytes := s;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION sh_items_live_bytes() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    UPDATE sites s SET data_bytes = s.data_bytes + d.n
      FROM (SELECT site_id, sum(octet_length(data::text)) AS n FROM sh_new
             WHERE deleted_at IS NULL GROUP BY site_id) d
     WHERE s.id = d.site_id;
  ELSIF TG_OP = 'DELETE' THEN
    UPDATE sites s SET data_bytes = s.data_bytes - d.n
      FROM (SELECT site_id, sum(octet_length(data::text)) AS n FROM sh_old
             WHERE deleted_at IS NULL GROUP BY site_id) d
     WHERE s.id = d.site_id;
  ELSE
    UPDATE sites s SET data_bytes = s.data_bytes + d.n
      FROM (SELECT site_id, sum(n) AS n FROM (
              SELECT site_id, octet_length(data::text) AS n FROM sh_new WHERE deleted_at IS NULL
              UNION ALL
              SELECT site_id, -octet_length(data::text) FROM sh_old WHERE deleted_at IS NULL) x
             GROUP BY site_id) d
     WHERE s.id = d.site_id AND d.n <> 0;
  END IF;
  RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS sites_state_bytes ON sites;
CREATE TRIGGER sites_state_bytes BEFORE INSERT OR UPDATE OF state ON sites
  FOR EACH ROW EXECUTE FUNCTION sh_sites_state_bytes();
DROP TRIGGER IF EXISTS collection_items_bytes_ins ON collection_items;
CREATE TRIGGER collection_items_bytes_ins AFTER INSERT ON collection_items
  REFERENCING NEW TABLE AS sh_new FOR EACH STATEMENT EXECUTE FUNCTION sh_items_live_bytes();
DROP TRIGGER IF EXISTS collection_items_bytes_upd ON collection_items;
CREATE TRIGGER collection_items_bytes_upd AFTER UPDATE ON collection_items
  REFERENCING OLD TABLE AS sh_old NEW TABLE AS sh_new FOR EACH STATEMENT EXECUTE FUNCTION sh_items_live_bytes();
DROP TRIGGER IF EXISTS collection_items_bytes_del ON collection_items;
CREATE TRIGGER collection_items_bytes_del AFTER DELETE ON collection_items
  REFERENCING OLD TABLE AS sh_old FOR EACH STATEMENT EXECUTE FUNCTION sh_items_live_bytes();

-- Backfill (a no-op on a new database). The UPDATE does not name state, so
-- the state trigger does not fire.
UPDATE sites s SET
  state_bytes = COALESCE(octet_length(s.state::text), 0),
  data_bytes  = COALESCE(octet_length(s.state::text), 0)
              + COALESCE((SELECT sum(octet_length(ci.data::text)) FROM collection_items ci
                           WHERE ci.site_id = s.id AND ci.deleted_at IS NULL), 0);

-- A change made with PATCH ops keeps only what it changed (diff: how to turn
-- the document after it back into the one before), with a full copy (prev)
-- at least every SAVED_DATA_SNAPSHOT_EVERY changes and on each day's first.
ALTER TABLE data_history ADD COLUMN IF NOT EXISTS diff JSONB;

-- Idempotency-Key answers keep the status, the version or item id (ref) and
-- a hash of the request body (a reused key with another body is refused),
-- never the response body. site_id bounds the rows kept per site.
ALTER TABLE idempotency_keys ADD COLUMN IF NOT EXISTS site_id UUID REFERENCES sites(id) ON DELETE CASCADE;
ALTER TABLE idempotency_keys ADD COLUMN IF NOT EXISTS ref BIGINT;
ALTER TABLE idempotency_keys ADD COLUMN IF NOT EXISTS body_hash BYTEA;
CREATE INDEX IF NOT EXISTS idx_idempotency_keys_site ON idempotency_keys (site_id, created_at);

-- What a site's history holds (every earlier value and diff), kept by the
-- trigger below in the same transaction as every history write, so a write
-- that pushes it past SAVED_DATA_HISTORY_MAX_MB thins that site's history
-- right away instead of waiting for the sweep. A row's size is the text of
-- its value (prev) or diff, the same measure the thinning uses.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS history_bytes BIGINT NOT NULL DEFAULT 0;

CREATE OR REPLACE FUNCTION sh_history_bytes() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    UPDATE sites s SET history_bytes = s.history_bytes + d.n
      FROM (SELECT site_id, sum(COALESCE(octet_length(prev::text), octet_length(diff::text), 0)) AS n
              FROM sh_hnew GROUP BY site_id) d
     WHERE s.id = d.site_id AND d.n <> 0;
  ELSIF TG_OP = 'DELETE' THEN
    UPDATE sites s SET history_bytes = s.history_bytes - d.n
      FROM (SELECT site_id, sum(COALESCE(octet_length(prev::text), octet_length(diff::text), 0)) AS n
              FROM sh_hold GROUP BY site_id) d
     WHERE s.id = d.site_id AND d.n <> 0;
  ELSE
    UPDATE sites s SET history_bytes = s.history_bytes + d.n
      FROM (SELECT site_id, sum(n) AS n FROM (
              SELECT site_id, COALESCE(octet_length(prev::text), octet_length(diff::text), 0) AS n FROM sh_hnew
              UNION ALL
              SELECT site_id, -COALESCE(octet_length(prev::text), octet_length(diff::text), 0) FROM sh_hold) x
             GROUP BY site_id) d
     WHERE s.id = d.site_id AND d.n <> 0;
  END IF;
  RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS data_history_bytes_ins ON data_history;
CREATE TRIGGER data_history_bytes_ins AFTER INSERT ON data_history
  REFERENCING NEW TABLE AS sh_hnew FOR EACH STATEMENT EXECUTE FUNCTION sh_history_bytes();
DROP TRIGGER IF EXISTS data_history_bytes_upd ON data_history;
CREATE TRIGGER data_history_bytes_upd AFTER UPDATE ON data_history
  REFERENCING OLD TABLE AS sh_hold NEW TABLE AS sh_hnew FOR EACH STATEMENT EXECUTE FUNCTION sh_history_bytes();
DROP TRIGGER IF EXISTS data_history_bytes_del ON data_history;
CREATE TRIGGER data_history_bytes_del AFTER DELETE ON data_history
  REFERENCING OLD TABLE AS sh_hold FOR EACH STATEMENT EXECUTE FUNCTION sh_history_bytes();

-- Backfill (a no-op on a new database). The triggers above lock data_history
-- until the end of the transaction, so no history write slips in between.
UPDATE sites s SET history_bytes = COALESCE((
  SELECT sum(COALESCE(octet_length(h.prev::text), octet_length(h.diff::text), 0))
    FROM data_history h WHERE h.site_id = s.id), 0);

-- Frozen legacy per-site hostnames (e.g. mysite.simple-host.app) bound to a
-- site_id. Populated by a later backfill; not wired into request paths yet.
-- Old handles kept after an operator rename, so links naming them resolve.
-- user_id NULL = retired: the handle of a deleted account, held by nobody
-- so no one else can take it (cp-gdpr-retired-handles.sql).
CREATE TABLE IF NOT EXISTS handle_aliases (
  handle     TEXT PRIMARY KEY,
  user_id    UUID REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A released <name>.<SITE_DOMAIN> stays with its site (302 to its current
-- address) and outlives it (site_id NULL: "this site was removed").
CREATE TABLE legacy_hostnames (
  hostname   TEXT PRIMARY KEY,
  site_id    UUID REFERENCES sites(id) ON DELETE SET NULL,
  user_id    UUID REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_legacy_hostnames_site ON legacy_hostnames(site_id);
CREATE UNIQUE INDEX IF NOT EXISTS sites_previous_domain_key ON sites (previous_domain) WHERE previous_domain IS NOT NULL;

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
  purpose    TEXT NOT NULL DEFAULT 'dashboard',
  site_id    UUID REFERENCES sites(id) ON DELETE CASCADE,
  CONSTRAINT auth_tokens_purpose_check CHECK (purpose IN ('dashboard', 'visitor') AND ((purpose = 'visitor') = (site_id IS NOT NULL))),
  expires_at TIMESTAMPTZ NOT NULL,
  used_at    TIMESTAMPTZ,
  attempts   INT DEFAULT 0,
  created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX auth_tokens_email_purpose_idx ON auth_tokens (email, purpose, site_id, created_at DESC);

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
-- Event hostnames handed to a hackathon organiser, pointing at THEIR server.
--
-- Rows exist so teardown knows which records to delete. A record left pointing
-- at a released cloud address is a subdomain takeover, so expires_at gives a
-- sweep something to act on when an organiser never tears down.
CREATE TABLE IF NOT EXISTS event_domains (
  name         TEXT NOT NULL,
  domain       TEXT NOT NULL,
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  ip           TEXT NOT NULL,
  record_ids   TEXT[] NOT NULL DEFAULT '{}',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (name, domain)
);
CREATE INDEX IF NOT EXISTS event_domains_expiry_idx ON event_domains (expires_at);
CREATE INDEX IF NOT EXISTS event_domains_user_idx ON event_domains (user_id);

CREATE TABLE IF NOT EXISTS analytics_ingest_state (
  logfile      TEXT PRIMARY KEY,
  offset_bytes BIGINT NOT NULL DEFAULT 0,
  inode        BIGINT NOT NULL DEFAULT 0,
  updated_at   TIMESTAMPTZ DEFAULT now()
);

-- Per-endpoint API traffic, for the admin page. Go middleware aggregates in
-- memory and flushes here; caller IPs are stored truncated (IPv4 /24, IPv6
-- /48) and pruned after 30 days. Mirrors db/migrations/api-analytics.sql;
-- db/migrations/truncate-api-ip.sql truncates rows written before that.
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

-- Caller geo (country/city/network) is NOT stored: it is resolved on this box
-- from local DB-IP Lite files when the admin page asks (internal/geoip). The
-- old ip_geo cache table is gone from fresh installs; see
-- db/migrations/local-geo.sql for existing databases.

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

-- Top pages and where visitors came from (owner analytics), people only:
-- views per site-relative path, and per referring domain. The domain is all
-- that is kept of a referrer (nginx logs only the host; no full URL, no
-- query); a link from the site's own address is not counted. Same retention
-- as the other aggregates (ANALYTICS_RETENTION_DAYS).
CREATE TABLE IF NOT EXISTS site_page_daily (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day     DATE NOT NULL,   -- UTC
  path    TEXT NOT NULL,   -- site-relative, no query string, at most 200 bytes
  views   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, day, path)
);
CREATE INDEX IF NOT EXISTS site_page_daily_day_idx ON site_page_daily (day);
CREATE TABLE IF NOT EXISTS site_referrer_daily (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day     DATE NOT NULL,   -- UTC
  domain  TEXT NOT NULL,   -- referring host name only, lowercased
  views   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, day, domain)
);
CREATE INDEX IF NOT EXISTS site_referrer_daily_day_idx ON site_referrer_daily (day);

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
  -- Site purpose: SHA-256 (base64url) of the __Host- nonce cookie set on the
  -- site host that started this sign-in (visitor-signin-nonce.sql).
  nonce_hash     TEXT,
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
  -- The starting browser's nonce hash; establish needs the matching cookie.
  nonce_hash   TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL,
  used_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS visitor_establish_tokens_expires_idx
  ON visitor_establish_tokens (expires_at);

ALTER TABLE oauth_states
  ALTER COLUMN site_id DROP NOT NULL;

ALTER TABLE oauth_states
  ADD COLUMN IF NOT EXISTS nonce_hash TEXT;
ALTER TABLE visitor_establish_tokens
  ADD COLUMN IF NOT EXISTS nonce_hash TEXT;

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

CREATE TABLE IF NOT EXISTS instance_config (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── Connector: OAuth 2.1 for the remote MCP endpoint ─────────────────────
-- Mirrors db/migrations/oauth-connector.sql; see there for the reasoning.
CREATE TABLE IF NOT EXISTS oauth_clients (
  client_id                  TEXT PRIMARY KEY,
  client_secret_hash         TEXT,
  client_name                TEXT NOT NULL,
  redirect_uris              TEXT[] NOT NULL,
  token_endpoint_auth_method TEXT NOT NULL DEFAULT 'none',
  -- FALSE only for an operator-registered confidential client whose platform
  -- cannot send PKCE (ChatGPT GPT Actions). Every self-registered client and
  -- every public client must use PKCE S256.
  pkce_required              BOOLEAN NOT NULL DEFAULT TRUE,
  -- TRUE for RFC 7591 self-registration; FALSE for clients created by the
  -- operator (`simple-host oauth-client create`), which the sweep never removes.
  dynamic                    BOOLEAN NOT NULL DEFAULT TRUE,
  created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at               TIMESTAMPTZ,
  CONSTRAINT oauth_clients_auth_method_check
    CHECK (token_endpoint_auth_method IN ('none', 'client_secret_post', 'client_secret_basic')),
  CONSTRAINT oauth_clients_secret_shape
    CHECK ((token_endpoint_auth_method = 'none') = (client_secret_hash IS NULL)),
  CONSTRAINT oauth_clients_pkce_check
    CHECK (pkce_required OR (client_secret_hash IS NOT NULL AND NOT dynamic))
);
CREATE INDEX IF NOT EXISTS oauth_clients_created_idx ON oauth_clients (created_at);

-- One grant per "Allow": a person connected an app. It is the refresh-token
-- family: rotating a refresh token stays inside it, and reuse of a rotated
-- token deletes it (and with it every token it issued). Disconnecting an app
-- deletes its grants.
CREATE TABLE IF NOT EXISTS oauth_grants (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  client_id    TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
  scope        TEXT NOT NULL,
  resource     TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  device       TEXT   -- the consent page's browser, summarised ("Chrome on macOS"); never the user agent or an IP
);
CREATE INDEX IF NOT EXISTS oauth_grants_user_idx ON oauth_grants (user_id, client_id);
CREATE INDEX IF NOT EXISTS oauth_grants_client_idx ON oauth_grants (client_id);

-- Authorization codes: single use, ~60 seconds, bound to the client, the
-- redirect URI, the PKCE challenge and the resource. grant_id is set when the
-- code is redeemed, so a second redemption can revoke what the first issued.
CREATE TABLE IF NOT EXISTS oauth_codes (
  code_hash      TEXT PRIMARY KEY,
  client_id      TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
  user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  redirect_uri   TEXT NOT NULL,
  code_challenge TEXT NOT NULL,
  scope          TEXT NOT NULL,
  resource       TEXT NOT NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at     TIMESTAMPTZ NOT NULL,
  used_at        TIMESTAMPTZ,
  grant_id       UUID REFERENCES oauth_grants(id) ON DELETE SET NULL,
  device         TEXT   -- copied onto the grant it becomes
);
CREATE INDEX IF NOT EXISTS oauth_codes_expires_idx ON oauth_codes (expires_at);

-- Access tokens (~1 hour) and refresh tokens (rotating, long-lived).
-- used_at on a refresh token marks it rotated; presenting it again is reuse.
CREATE TABLE IF NOT EXISTS oauth_tokens (
  token_hash TEXT PRIMARY KEY,
  grant_id   UUID NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  used_at    TIMESTAMPTZ,
  CONSTRAINT oauth_tokens_kind_check CHECK (kind IN ('access', 'refresh'))
);
CREATE INDEX IF NOT EXISTS oauth_tokens_grant_idx ON oauth_tokens (grant_id);
CREATE INDEX IF NOT EXISTS oauth_tokens_expires_idx ON oauth_tokens (expires_at);

-- Sign-in links are bound to the browser that asked for them (login-CSRF fix,
-- same review). The page that requests an emailed code, or starts Google
-- sign-in, keeps a random nonce and sends its SHA-256 (base64url); the link
-- token is stored with that hash and /v1/auth/verify redeems a token only
-- together with the nonce itself. A link token without a hash is never
-- redeemable; the typed 6-digit code is unaffected.
ALTER TABLE auth_tokens ADD COLUMN IF NOT EXISTS nonce_hash TEXT;

-- Saved data, step 2: kinds (mirrors db/migrations/sd2-saved-data-kinds.sql).
-- A declared name's kind (content = Page info, entries = Submissions; NULL =
-- not declared), one entry per person, and the owner's email choice. The
-- private column above is the Submissions visibility.
ALTER TABLE collection_settings ADD COLUMN IF NOT EXISTS kind TEXT;
ALTER TABLE collection_settings ADD COLUMN IF NOT EXISTS one_per_person BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE collection_settings ADD COLUMN IF NOT EXISTS notify TEXT NOT NULL DEFAULT 'off';
ALTER TABLE collection_settings ADD COLUMN IF NOT EXISTS notify_sent_at TIMESTAMPTZ;
ALTER TABLE collection_settings ADD COLUMN IF NOT EXISTS declared_at TIMESTAMPTZ;
-- Who may save: 'anyone' (anyone who signs in) or 'listed' (the allow list
-- below); the block list applies in both modes.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS savers_mode TEXT NOT NULL DEFAULT 'anyone';
CREATE TABLE IF NOT EXISTS site_savers (
  site_id  UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  list     TEXT NOT NULL CHECK (list IN ('allow', 'block')),
  pattern  TEXT NOT NULL,
  added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (site_id, list, pattern)
);
CREATE INDEX IF NOT EXISTS idx_collection_items_mine
  ON collection_items (site_id, collection, submitted_by, id DESC) WHERE submitted_by IS NOT NULL;

-- Saved data, steps 3 and 4 (mirrors db/migrations/sd3-saved-data-personal-board.sql):
-- kinds mine (Personal: one private record per signed-in person per name, the
-- row's submitted_by) and board (Shared board: a list signed-in visitors edit
-- together). version counts an item's changes, for a board's If-Match check.
ALTER TABLE collection_items ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1;

-- Which db/migrations/ files `simple-host migrate` has applied (or an operator
-- recorded with `migrate -mark`). A database built from this file already has
-- every migration's effect; migrate still runs each new (idempotent) file once
-- and records it. Historical hand-applied files are never recorded here.
-- New custom-domain certificates asked for, per account (a cap of a few a day).
CREATE TABLE IF NOT EXISTS domain_cert_requests (
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  domain       TEXT NOT NULL,
  requested_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS domain_cert_requests_user ON domain_cert_requests (user_id, requested_at);

-- "Ask about this page": questions answered per UTC day, across everyone.
CREATE TABLE IF NOT EXISTS ask_daily (
  day   DATE PRIMARY KEY,
  count INTEGER NOT NULL DEFAULT 0
);

-- The setup helper's "Check my choices" count per UTC day
-- (SETUP_CHECK_DAILY_MAX). Nothing about who asked or what.
CREATE TABLE IF NOT EXISTS setup_check_daily (
  day   DATE PRIMARY KEY,
  count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS schema_migrations (
  name       TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
