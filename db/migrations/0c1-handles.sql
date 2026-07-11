-- Phase 0c-1 additive migration: URL-safe user handles.
-- Additive only. handle is nullable+unique; backfilled below from the email local-part.
ALTER TABLE users ADD COLUMN IF NOT EXISTS handle TEXT UNIQUE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS handle_changed_at TIMESTAMPTZ;

-- Backfill handles for existing users.
-- admin -> 'admin'; email users -> sanitized local-part ([a-z0-9-], lowercased),
-- de-duplicated by appending the short id suffix on collision.
UPDATE users SET handle = 'admin' WHERE username = 'admin' AND handle IS NULL;

UPDATE users u SET handle = base.h
FROM (
  SELECT id,
         regexp_replace(lower(split_part(username,'@',1)), '[^a-z0-9-]', '-', 'g') AS h
  FROM users
  WHERE username <> 'admin'
) base
WHERE u.id = base.id AND u.handle IS NULL
  AND NOT EXISTS (SELECT 1 FROM users x WHERE x.handle = base.h);

-- Collision fallback: any still-null handle gets local-part + '-' + first 6 of id.
UPDATE users u
SET handle = regexp_replace(lower(split_part(username,'@',1)), '[^a-z0-9-]', '-', 'g')
             || '-' || substr(u.id::text, 1, 6)
WHERE u.handle IS NULL AND u.username <> 'admin';
