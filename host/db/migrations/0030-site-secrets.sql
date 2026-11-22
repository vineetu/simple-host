-- Site secrets (SEC-1): a key an owner saves for one site, such as an API key
-- for an outside service. Write-only: the value is sealed with AES-256-GCM
-- under the master key file (SECRETS_KEY_FILE), the site id and name bound as
-- additional data, and is never shown again. destination is the one outside
-- host it may ever be sent to; who may trigger that use is owner, signed-in
-- or anyone, always the owner's own choice (no default). Removing the site
-- for good removes its secrets. attach says how the key goes with each
-- request a site's page asks for: as the header or query parameter
-- attach_name, after attach_prefix (such as "Bearer ").
--
-- Additive and idempotent: one new table. Safe while the previous program is
-- running: it never reads it.

CREATE TABLE IF NOT EXISTS site_secrets (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  site_id     UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  ciphertext  BYTEA NOT NULL,
  nonce       BYTEA NOT NULL,
  key_version INTEGER NOT NULL DEFAULT 1,
  destination TEXT NOT NULL,
  who         TEXT NOT NULL CHECK (who IN ('owner', 'signed-in', 'anyone')),
  attach        TEXT NOT NULL CHECK (attach IN ('header', 'query')),
  attach_name   TEXT NOT NULL,
  attach_prefix TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at TIMESTAMPTZ,
  use_count   BIGINT NOT NULL DEFAULT 0,
  UNIQUE (site_id, name)
);
