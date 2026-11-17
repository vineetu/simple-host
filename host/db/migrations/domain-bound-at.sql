ALTER TABLE sites ADD COLUMN IF NOT EXISTS domain_bound_at TIMESTAMPTZ;
UPDATE sites SET domain_bound_at = COALESCE(domain_verified_at, now()) WHERE custom_domain IS NOT NULL AND domain_bound_at IS NULL;
