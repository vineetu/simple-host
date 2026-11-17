-- Browser sign-in pass. Deleting a parent account key cascades to every
-- pass and site session it minted. No owner API accepts this credential.
CREATE TABLE IF NOT EXISTS sso_passes (
  id_hash TEXT PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  parent_key_hash TEXT NOT NULL REFERENCES api_keys(key_hash) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  idle_expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS sso_passes_parent_idx ON sso_passes(parent_key_hash);
ALTER TABLE visitor_sessions ADD COLUMN IF NOT EXISTS sso_pass_hash TEXT REFERENCES sso_passes(id_hash) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS visitor_sessions_sso_idx ON visitor_sessions(sso_pass_hash) WHERE sso_pass_hash IS NOT NULL;
CREATE TABLE IF NOT EXISTS sso_consents (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, site_id)
);
CREATE TABLE IF NOT EXISTS sso_hops (
  state TEXT PRIMARY KEY,
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  host TEXT NOT NULL,
  return_to TEXT NOT NULL,
  nonce_hash TEXT NOT NULL,
  consent_hash TEXT,
  consent_pass_hash TEXT,
  expires_at TIMESTAMPTZ NOT NULL
);
-- Kept after revocation so a site can clear the apex cookie by navigation.
CREATE TABLE IF NOT EXISTS sso_signouts (
  token TEXT PRIMARY KEY,
  pass_hash TEXT NOT NULL,
  return_to TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL
);
