-- Phase 0b additive migration: legacy_hostnames table + backfill.
-- Additive only; no existing data touched. Safe to re-run (IF NOT EXISTS / ON CONFLICT).
CREATE TABLE IF NOT EXISTS legacy_hostnames (
  hostname   TEXT PRIMARY KEY,
  site_id    UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  user_id    UUID REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_legacy_hostnames_site ON legacy_hostnames(site_id);

-- Backfill: every existing site's frozen legacy hostname is <name>.<SITE_DOMAIN>.
-- SITE_DOMAIN on this box = d.agent-deploy.dev.
INSERT INTO legacy_hostnames (hostname, site_id, user_id)
SELECT lower(name) || '.d.agent-deploy.dev', id, user_id
FROM sites
ON CONFLICT (hostname) DO NOTHING;
