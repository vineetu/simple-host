-- sites: sites, their versions and addresses, visit numbers and reports.

CREATE TABLE sites (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id          UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  name              TEXT NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  changed_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  live_version      INTEGER NOT NULL DEFAULT 0,
  listed            BOOLEAN NOT NULL DEFAULT false,
  offline           BOOLEAN NOT NULL DEFAULT false,
  deleted_at        TIMESTAMPTZ,
  taken_down_at     TIMESTAMPTZ,
  taken_down_reason TEXT NOT NULL DEFAULT '',
  access            TEXT NOT NULL DEFAULT 'open' CHECK (access IN ('open', 'passcode', 'viewers')),
  passcode_enc      BYTEA,
  passcode_gen      INTEGER NOT NULL DEFAULT 0,
  last_visit_at     TIMESTAMPTZ,
  idle_warned_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX sites_owner_name ON sites (owner_id, name) WHERE deleted_at IS NULL;

ALTER TABLE names ADD FOREIGN KEY (site_id) REFERENCES sites(id) ON DELETE SET NULL;
ALTER TABLE visitor_sessions ADD FOREIGN KEY (site_id) REFERENCES sites(id) ON DELETE CASCADE;
ALTER TABLE visitor_signins ADD FOREIGN KEY (site_id) REFERENCES sites(id) ON DELETE CASCADE;

-- Files live at <data dir>/sites/<site id>/v<n>/ and never change after writing.
CREATE TABLE versions (
  site_id    UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  n          INTEGER NOT NULL,
  author_id  UUID REFERENCES people(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  bytes      BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, n)
);

-- Named viewers. A viewer is a person (an email), with or without a handle.
CREATE TABLE viewers (
  site_id   UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  person_id UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  added_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (site_id, person_id)
);

-- Own domains (kind own: one host, one site) and shared domains (kind shared:
-- <label>.<host> answers for the owner's site named <prefix><label>).
CREATE TABLE hostnames (
  host            TEXT PRIMARY KEY,
  kind            TEXT NOT NULL CHECK (kind IN ('own', 'shared')),
  site_id         UUID REFERENCES sites(id) ON DELETE CASCADE,
  owner_id        UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  prefix          TEXT NOT NULL DEFAULT '',
  proof_token     TEXT NOT NULL DEFAULT '',
  proven_at       TIMESTAMPTZ,
  status          TEXT NOT NULL DEFAULT 'waiting',
  cert_status     TEXT NOT NULL DEFAULT 'none',
  cert_expires_at TIMESTAMPTZ,
  error           TEXT NOT NULL DEFAULT '',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((kind = 'own') = (site_id IS NOT NULL))
);

-- app_shape: an account from the old service, whose sites answer at
-- <site>.<handle>.simple-host.app. Otherwise <site>--<handle>.simple-host.site.
CREATE TABLE owner_addresses (
  owner_id     UUID PRIMARY KEY REFERENCES people(id) ON DELETE CASCADE,
  home_site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
  app_shape    BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE upload_links (
  token_hash TEXT PRIMARY KEY,
  site_id    UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  person_id  UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL CHECK (kind IN ('archive', 'files')),
  namespace  TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  used_at    TIMESTAMPTZ
);

CREATE TABLE stats_daily (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day     DATE NOT NULL,
  views   INTEGER NOT NULL DEFAULT 0,
  people  INTEGER NOT NULL DEFAULT 0,
  bots    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, day)
);

CREATE TABLE stats_keys (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day     DATE NOT NULL,
  kind    TEXT NOT NULL CHECK (kind IN ('page', 'referrer', 'country')),
  key     TEXT NOT NULL,
  n       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, day, kind, key)
);

CREATE TABLE reports (
  id         BIGSERIAL PRIMARY KEY,
  url        TEXT NOT NULL,
  reason     TEXT NOT NULL,
  site_id    UUID REFERENCES sites(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  handled_at TIMESTAMPTZ
);
