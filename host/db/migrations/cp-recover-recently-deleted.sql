-- Recently deleted (completeness plan, 2026-09-27): deleting a site keeps it for
-- 7 days. The row stays (so its versions, saved data, collections, private-list
-- settings and claimed names stay with it, and its name stays held); deleted_at
-- marks it gone. Every lookup that serves or lists a site skips such rows, and an
-- in-process sweep removes them for good once the window has passed.
--
-- Apply once to an existing database, as the table owner, BEFORE deploying the
-- build that reads it (the server refuses to start without this column):
--   sudo -u postgres psql -d simplehost -c 'SET ROLE simplehost' -f db/migrations/cp-recover-recently-deleted.sql
-- Idempotent. Adding a nullable column with no default is a metadata-only change.

ALTER TABLE sites ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_sites_deleted_at ON sites (deleted_at) WHERE deleted_at IS NOT NULL;
