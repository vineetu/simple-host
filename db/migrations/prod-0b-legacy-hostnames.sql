CREATE TABLE IF NOT EXISTS legacy_hostnames (
  hostname   TEXT PRIMARY KEY,
  site_id    UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  user_id    UUID REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_legacy_hostnames_site ON legacy_hostnames(site_id);
INSERT INTO legacy_hostnames (hostname, site_id, user_id)
SELECT lower(name) || '.simple-host.app', id, user_id FROM sites
ON CONFLICT (hostname) DO NOTHING;
