ALTER TABLE site_storage_resources ADD COLUMN IF NOT EXISTS kv_store TEXT NOT NULL DEFAULT 'postgres' CHECK (kv_store IN ('postgres','sqlite'));
