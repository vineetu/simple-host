-- Spam-checked posting (2026-10-14): the comments preset lets anyone add
-- once Cloudflare Turnstile passes (acc_add 'checked'), and approve_first
-- holds those posts until the owner approves them.
ALTER TABLE site_storage_resources DROP CONSTRAINT IF EXISTS site_storage_resources_acc_add_check;
ALTER TABLE site_storage_resources ADD CONSTRAINT site_storage_resources_acc_add_check CHECK (acc_add IN ('nobody','owner','signed-in','checked','anyone'));
ALTER TABLE site_storage_tables DROP CONSTRAINT IF EXISTS site_storage_tables_acc_add_check;
ALTER TABLE site_storage_tables ADD CONSTRAINT site_storage_tables_acc_add_check CHECK (acc_add IN ('nobody','owner','signed-in','checked','anyone'));
ALTER TABLE site_storage_resources ADD COLUMN IF NOT EXISTS approve_first BOOLEAN NOT NULL DEFAULT FALSE;
