-- Phase 0e: names become per-user. Applied to the live DB (additive-then-swap; no data loss).
BEGIN;
ALTER TABLE sites DROP CONSTRAINT IF EXISTS sites_name_key;
ALTER TABLE sites ADD CONSTRAINT sites_user_name UNIQUE (user_id, name);
-- site_url backfill to the v3 path URL (SITE_DOMAIN=d.agent-deploy.dev -> content host sites.d.agent-deploy.dev)
UPDATE sites s SET site_url = 'https://sites.d.agent-deploy.dev/' || u.handle || '/' || s.name || '/'
FROM users u WHERE u.id = s.user_id AND u.handle IS NOT NULL;
COMMIT;
SELECT conname, pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='sites'::regclass AND contype='u';
SELECT name, site_url FROM sites ORDER BY name;
