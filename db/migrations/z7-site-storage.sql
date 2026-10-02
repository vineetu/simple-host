-- Additive, owner-configured site storage primitives (2026-10-02).
CREATE TABLE IF NOT EXISTS site_storage_resources (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('kv','sqlite','files')),
  read_policy TEXT NOT NULL DEFAULT 'owner' CHECK (read_policy IN ('anyone','signed-in','owner')),
  write_policy TEXT NOT NULL DEFAULT 'owner' CHECK (write_policy IN ('anyone','signed-in','owner')),
  site_passcode TEXT NOT NULL DEFAULT 'inherit' CHECK (site_passcode IN ('inherit','off')),
  PRIMARY KEY (site_id,name)
);
CREATE TABLE IF NOT EXISTS site_storage_kv (
  site_id UUID NOT NULL,
  resource_name TEXT NOT NULL,
  key TEXT NOT NULL,
  value JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (site_id,resource_name,key),
  FOREIGN KEY (site_id,resource_name) REFERENCES site_storage_resources(site_id,name) ON DELETE CASCADE
);
