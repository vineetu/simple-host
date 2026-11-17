-- cp-ops (2026-09-27): operator take-down of a site, and suspension of a person.
-- Additive and idempotent: nullable columns without defaults are a catalog-only
-- change in Postgres (no table rewrite, brief lock). Safe on a live database.
-- The binary refuses to start until these exist (internal/db/schemacheck.go).
ALTER TABLE sites ADD COLUMN IF NOT EXISTS suspended_at TIMESTAMPTZ;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS suspended_reason TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS suspended_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS suspended_reason TEXT;
