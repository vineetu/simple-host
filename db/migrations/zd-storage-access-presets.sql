-- Storage access presets (2026-10-10): an access matrix per resource and,
-- for SQLite, per table. NULL acc_* is a resource saved before presets; it
-- keeps its older read/write/write_mode rules until the owner saves a policy.
ALTER TABLE site_storage_resources ADD COLUMN IF NOT EXISTS acc_read TEXT CHECK (acc_read IN ('nobody','owner','own','signed-in','anyone'));
ALTER TABLE site_storage_resources ADD COLUMN IF NOT EXISTS acc_add TEXT CHECK (acc_add IN ('nobody','owner','signed-in','anyone'));
ALTER TABLE site_storage_resources ADD COLUMN IF NOT EXISTS acc_edit TEXT CHECK (acc_edit IN ('nobody','owner','own','signed-in'));
ALTER TABLE site_storage_resources ADD COLUMN IF NOT EXISTS acc_delete TEXT CHECK (acc_delete IN ('nobody','owner','own','signed-in'));
ALTER TABLE site_storage_resources ADD COLUMN IF NOT EXISTS preset_base TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS site_storage_tables (
 site_id UUID NOT NULL,
 resource_name TEXT NOT NULL,
 table_name TEXT NOT NULL,
 acc_read TEXT NOT NULL CHECK (acc_read IN ('nobody','owner','own','signed-in','anyone')),
 acc_add TEXT NOT NULL CHECK (acc_add IN ('nobody','owner','signed-in','anyone')),
 acc_edit TEXT NOT NULL CHECK (acc_edit IN ('nobody','owner','own','signed-in')),
 acc_delete TEXT NOT NULL CHECK (acc_delete IN ('nobody','owner','own','signed-in')),
 preset_base TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (site_id,resource_name,table_name),
 FOREIGN KEY (site_id,resource_name) REFERENCES site_storage_resources(site_id,name) ON DELETE CASCADE
);
ALTER TABLE site_storage_files ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE site_storage_kv ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();
