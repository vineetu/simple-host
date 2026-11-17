-- Custom domains go live on their own (completeness plan, cp/domains).
-- Additive only; safe to run on the live database and safe to re-run.
--
--   previous_domain           the proven address a site keeps serving at while
--                             a newly connected domain is still pending; cleared
--                             once the new one is verified with its certificate
--   domain_cert_status        pending | issuing | live | failed (NULL = none)
--   domain_failing_since      when a verified domain first failed its checks
--   domain_lapse_notified_at  when the owner was emailed about it
ALTER TABLE sites ADD COLUMN IF NOT EXISTS previous_domain TEXT;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS domain_cert_status TEXT;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS domain_failing_since TIMESTAMPTZ;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS domain_lapse_notified_at TIMESTAMPTZ;
CREATE UNIQUE INDEX IF NOT EXISTS sites_previous_domain_key ON sites (previous_domain) WHERE previous_domain IS NOT NULL;

-- A released <name>.<SITE_DOMAIN> stays with its site, and outlives it: when
-- the site is deleted the row keeps the name (site_id NULL) so it answers
-- "this site was removed" instead of passing to a stranger.
ALTER TABLE legacy_hostnames ALTER COLUMN site_id DROP NOT NULL;
ALTER TABLE legacy_hostnames DROP CONSTRAINT IF EXISTS legacy_hostnames_site_id_fkey;
ALTER TABLE legacy_hostnames ADD CONSTRAINT legacy_hostnames_site_id_fkey
  FOREIGN KEY (site_id) REFERENCES sites(id) ON DELETE SET NULL;
