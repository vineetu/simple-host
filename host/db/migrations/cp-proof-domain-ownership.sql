-- Custom domains need a TXT ownership record (2026-09-27).
--
-- Every site gets a random token; a custom domain counts as verified only while
-- the DNS TXT record _simple-host.<domain> holds it. Domains already verified
-- when this runs are grandfathered (domain_proof_exempt) for as long as they
-- stay verified; one that lapses needs the record to be connected again.
-- domain_cert_requests caps new certificate requests per account per day.
--
-- Apply once to an existing database, as the table owner, BEFORE deploying the
-- build that reads it (the server refuses to start without these columns), and
-- right before the deploy (a domain verified by the old build after this ran is
-- not grandfathered and will need the record):
--   sudo -u postgres psql -d simplehost -c 'SET ROLE simplehost' -f db/migrations/cp-proof-domain-ownership.sql
-- Idempotent. The column adds are metadata-only (the token default is set after
-- the column exists, so no table rewrite); the backfill touches each site row once.

ALTER TABLE sites ADD COLUMN IF NOT EXISTS domain_token TEXT;
ALTER TABLE sites ALTER COLUMN domain_token SET DEFAULT ('sh-' || replace(gen_random_uuid()::text, '-', ''));
UPDATE sites SET domain_token = 'sh-' || replace(gen_random_uuid()::text, '-', '') WHERE domain_token IS NULL;

ALTER TABLE sites ADD COLUMN IF NOT EXISTS domain_proof_exempt TEXT[] NOT NULL DEFAULT '{}';
UPDATE sites
SET domain_proof_exempt = array_remove(ARRAY[
        CASE WHEN domain_verified_at IS NOT NULL THEN custom_domain END,
        previous_domain], NULL)
WHERE domain_proof_exempt = '{}'
  AND ((custom_domain IS NOT NULL AND domain_verified_at IS NOT NULL) OR previous_domain IS NOT NULL);

CREATE TABLE IF NOT EXISTS domain_cert_requests (
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  domain       TEXT NOT NULL,
  requested_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS domain_cert_requests_user ON domain_cert_requests (user_id, requested_at);
