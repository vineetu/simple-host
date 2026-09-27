-- knobs (2026-09-27): the date a site owner was promised is stored when the
-- promise is made, so changing DELETED_RETENTION_DAYS, IDLE_GRACE_DAYS or
-- DOMAIN_LAPSE_HOURS later applies to new deletions and warnings only.
--   purge_at           a deleted site is removed for good (set on delete)
--   idle_remove_at     an idle-warned site moves to Recently deleted (set on the warning)
--   domain_release_at  a failing domain is disconnected (set on the emailed warning)
-- NULL (rows from before this file) falls back to the current setting.
--
-- Apply BEFORE deploying the build that reads it. Idempotent; nullable
-- columns with no default are catalog-only changes.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS purge_at TIMESTAMPTZ;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS idle_remove_at TIMESTAMPTZ;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS domain_release_at TIMESTAMPTZ;
