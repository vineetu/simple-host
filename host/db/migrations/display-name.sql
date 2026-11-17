-- A display name, separate from the handle.
--
-- handle is the URL path id: sites.<host>/<handle>/<site>/. Changing it moves
-- every published address, so it is fixed once an account owns a site.
-- display_name is only ever shown on screen, so it can change whenever.
ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name TEXT;
