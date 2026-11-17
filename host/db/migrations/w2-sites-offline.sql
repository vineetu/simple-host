-- w2-sites (2026-09-27): the owner can take a site offline. Every address then
-- answers a plain "This site is offline" page (503) and visitor saves are
-- refused; files, versions and saved data are kept, and the owner's agent can
-- still deploy and read. NULL = online. An `offline` marker file in the site
-- folder mirrors it for the servers that read files straight from disk.
--
-- Apply BEFORE deploying the build that reads it (the server refuses to start
-- without it). Idempotent; a nullable column with no default is a
-- catalog-only change.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS offline_at TIMESTAMPTZ;
