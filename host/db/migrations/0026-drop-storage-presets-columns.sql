-- The storage presets are gone: rules decide access. Drops what nothing reads
-- any more. Kept on purpose: site_storage_resources.read_policy, write_policy,
-- write_mode and acc_read, which still recognise a resource saved before
-- presets (acc_read IS NULL) and its legacy visitor-write behaviour.
-- Run only once the program that no longer touches these columns is running:
-- the previous program reads and writes them. Dropping a column drops its CHECK.
ALTER TABLE site_storage_resources
  DROP COLUMN IF EXISTS acc_add,
  DROP COLUMN IF EXISTS acc_edit,
  DROP COLUMN IF EXISTS acc_delete,
  DROP COLUMN IF EXISTS preset_base,
  DROP COLUMN IF EXISTS approve_first,
  DROP COLUMN IF EXISTS rules_converted_from,
  DROP COLUMN IF EXISTS kv_store;
ALTER TABLE site_storage_tables
  DROP COLUMN IF EXISTS acc_read,
  DROP COLUMN IF EXISTS acc_add,
  DROP COLUMN IF EXISTS acc_edit,
  DROP COLUMN IF EXISTS acc_delete,
  DROP COLUMN IF EXISTS preset_base;
DROP TABLE IF EXISTS site_storage_kv;
