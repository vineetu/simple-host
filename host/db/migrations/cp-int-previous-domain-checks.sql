-- Review fixes (cp/integrate, 2026-09-27): the earlier address a site keeps
-- serving while its new domain is pending is now checked too, with its own
-- failing clock; after 72 hours of failing it is let go.
--
-- Apply once to an existing database, as the table owner, BEFORE deploying the
-- build that reads it (the server refuses to start without this column):
--   sudo -u postgres psql -d simplehost -c 'SET ROLE simplehost' -f db/migrations/cp-int-previous-domain-checks.sql
-- Idempotent. Adding a nullable column with no default is a metadata-only change.

ALTER TABLE sites ADD COLUMN IF NOT EXISTS previous_domain_failing_since TIMESTAMPTZ;
