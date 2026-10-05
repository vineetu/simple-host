-- Add-only writes and per-person reads; older resources retain full writes.
ALTER TABLE site_storage_resources DROP CONSTRAINT IF EXISTS site_storage_resources_read_policy_check;
ALTER TABLE site_storage_resources ADD CONSTRAINT site_storage_resources_read_policy_check CHECK (read_policy IN ('anyone','signed-in','owner','own'));
ALTER TABLE site_storage_resources ADD COLUMN IF NOT EXISTS write_mode TEXT NOT NULL DEFAULT 'full' CHECK (write_mode IN ('full','add'));
ALTER TABLE site_storage_kv ADD COLUMN IF NOT EXISTS writer_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS site_storage_kv_writer_idx ON site_storage_kv(site_id,resource_name,writer_id,key);
CREATE TABLE IF NOT EXISTS site_storage_files (
 site_id UUID NOT NULL,
 resource_name TEXT NOT NULL,
 path TEXT NOT NULL,
 writer_id TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (site_id,resource_name,path),
 FOREIGN KEY (site_id,resource_name) REFERENCES site_storage_resources(site_id,name) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS site_storage_files_writer_idx ON site_storage_files(site_id,resource_name,writer_id,path);
