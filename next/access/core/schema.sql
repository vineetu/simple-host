-- access: people, names, and every credential that turns into a person.

-- A person is one email. A team is a row with is_team and no email.
CREATE TABLE people (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email            TEXT UNIQUE,
  display_name     TEXT NOT NULL DEFAULT '',
  is_team          BOOLEAN NOT NULL DEFAULT false,
  operator         BOOLEAN NOT NULL DEFAULT false,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  suspended_at     TIMESTAMPTZ,
  suspended_reason TEXT NOT NULL DEFAULT '',
  CHECK (is_team = (email IS NULL))
);

-- Handles and free short names: one label under the service's domains. A row
-- is never deleted; retired_at set means nobody can ever take the name again.
CREATE TABLE names (
  name       TEXT PRIMARY KEY,
  kind       TEXT NOT NULL CHECK (kind IN ('handle', 'short')),
  person_id  UUID REFERENCES people(id) ON DELETE SET NULL,
  site_id    UUID, -- short names; the foreign key is added in sites/core/schema.sql
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  retired_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX names_live_handle ON names (person_id) WHERE kind = 'handle' AND retired_at IS NULL;

-- Dashboard sessions. The cookie holds the secret; the row holds its hash.
CREATE TABLE sessions (
  id_hash      TEXT PRIMARY KEY,
  person_id    UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_person ON sessions (person_id);

CREATE TABLE sign_in_codes (
  id         BIGSERIAL PRIMARY KEY,
  email      TEXT NOT NULL,
  code_hash  TEXT NOT NULL,
  tries      INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  used_at    TIMESTAMPTZ
);
CREATE INDEX sign_in_codes_email ON sign_in_codes (email, created_at);

-- API keys: use Simple Host without a connector. hash is hex SHA-256 of the key, as the old service stored it.
CREATE TABLE keys (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  hash         TEXT NOT NULL UNIQUE,
  person_id    UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  last4        TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at TIMESTAMPTZ
);

CREATE TABLE team_members (
  team_id   UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  person_id UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (team_id, person_id)
);
CREATE INDEX team_members_person ON team_members (person_id);

CREATE TABLE team_invites (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  team_id     UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  email       TEXT NOT NULL,
  invited_by  UUID REFERENCES people(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  accepted_at TIMESTAMPTZ
);

-- AI app connections (OAuth). Secrets and tokens are stored as hex SHA-256.
CREATE TABLE oauth_clients (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL DEFAULT '',
  redirect_uris TEXT[] NOT NULL DEFAULT '{}',
  secret_hash   TEXT,
  metadata      JSONB NOT NULL DEFAULT '{}',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE oauth_grants (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  person_id  UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  client_id  TEXT NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
  scope      TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (person_id, client_id)
);

CREATE TABLE oauth_codes (
  code_hash      TEXT PRIMARY KEY,
  client_id      TEXT NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
  person_id      UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  redirect_uri   TEXT NOT NULL,
  code_challenge TEXT NOT NULL,
  scope          TEXT NOT NULL DEFAULT '',
  expires_at     TIMESTAMPTZ NOT NULL,
  used_at        TIMESTAMPTZ
);

CREATE TABLE oauth_tokens (
  hash       TEXT PRIMARY KEY,
  kind       TEXT NOT NULL CHECK (kind IN ('access', 'refresh')),
  grant_id   UUID NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE,
  person_id  UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX oauth_tokens_grant ON oauth_tokens (grant_id);

-- A visitor's sign-in on one site. id is the SHA-256 of the cookie's bytes (the
-- cookie, __Host-sh_vsess, is their hex): access.SiteSessionID.
CREATE TABLE visitor_sessions (
  id           BYTEA PRIMARY KEY,
  person_id    UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  site_id      UUID NOT NULL, -- foreign key added in sites/core/schema.sql
  host         TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX visitor_sessions_person ON visitor_sessions (person_id);

-- Who signed in on which site, kept after sessions end. confirmed: the person
-- confirmed this site once, so signing in again needs no question.
CREATE TABLE visitor_signins (
  site_id   UUID NOT NULL, -- foreign key added in sites/core/schema.sql
  person_id UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  first_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  confirmed BOOLEAN NOT NULL DEFAULT false,
  PRIMARY KEY (site_id, person_id)
);
