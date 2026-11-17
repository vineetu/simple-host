-- R2-S1: nullable rule sets and the original configuration retained by conversion.
ALTER TABLE site_storage_resources ADD COLUMN IF NOT EXISTS rules JSONB;
ALTER TABLE site_storage_tables ADD COLUMN IF NOT EXISTS rules JSONB;
ALTER TABLE site_storage_resources ADD COLUMN IF NOT EXISTS rules_converted_from JSONB;
