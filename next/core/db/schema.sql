-- ops and core: events between features, notifications, downloads, admin counts.

-- One row per thing the owner may be told about (kind 'data' or 'access').
-- ops/notify reads and delivers them.
CREATE TABLE events (
  id        BIGSERIAL PRIMARY KEY,
  at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  kind      TEXT NOT NULL,
  owner_id  UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  site_id   UUID REFERENCES sites(id) ON DELETE CASCADE,
  body      JSONB NOT NULL DEFAULT '{}',
  pushed_at TIMESTAMPTZ
);
CREATE INDEX events_owner ON events (owner_id, id);

-- What a site's owner wants to be told about: data changes in one table or
-- file store of a namespace, or someone asking for access.
CREATE TABLE notify_rules (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  site_id    UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL CHECK (kind IN ('data', 'access')),
  namespace  TEXT NOT NULL DEFAULT '',
  target     TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE push_devices (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  person_id  UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  endpoint   TEXT NOT NULL,
  p256dh     TEXT NOT NULL,
  auth       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (person_id, endpoint)
);

-- A download of everything a person has. The link's token is made when the
-- download is ready, and only its hash is kept.
CREATE TABLE exports (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  token_hash TEXT UNIQUE,
  person_id  UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  status     TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'ready', 'failed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ready_at   TIMESTAMPTZ,
  expires_at TIMESTAMPTZ
);

CREATE TABLE api_daily (
  day     DATE NOT NULL,
  country TEXT NOT NULL DEFAULT '',
  calls   INTEGER NOT NULL DEFAULT 0,
  people  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (day, country)
);

CREATE TABLE net_usage_daily (
  day       DATE PRIMARY KEY,
  bytes_in  BIGINT NOT NULL DEFAULT 0,
  bytes_out BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE schema_version (
  version    INTEGER PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
