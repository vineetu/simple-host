BEGIN;
ALTER TABLE sites DROP CONSTRAINT IF EXISTS sites_name_key;
ALTER TABLE sites ADD CONSTRAINT sites_user_name UNIQUE (user_id, name);
UPDATE sites s SET site_url = 'https://sites.simple-host.app/' || u.handle || '/' || s.name || '/'
FROM users u WHERE u.id = s.user_id AND u.handle IS NOT NULL;
COMMIT;
