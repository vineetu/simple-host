-- v0.7.7 (2026-09-29): address families (owner decision 2026-09-29). An
-- account connects *.<suffix> once; every site of the account named
-- <site_prefix><label> then also answers at <label>.<suffix>. A family serves
-- nothing until it is verified (TXT _simple-host.<suffix> = token, or
-- proof_exempt set by the admin, plus the wildcard pointing here) and its
-- certificate is live. Release 1: cert_mode 'wildcard' only (the operator's
-- wildcard certificate, named by cert_name); 'per_host' is reserved for later.
-- family_cert_requests is the per-account cap for per-host certificates
-- (unused in release 1).
--
-- Apply BEFORE deploying the build that reads it (the server refuses to start
-- without it). Idempotent; new tables only.
CREATE TABLE IF NOT EXISTS address_families (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  suffix            TEXT NOT NULL,
  site_prefix       TEXT NOT NULL DEFAULT '',
  rank              INTEGER NOT NULL DEFAULT 0,
  canonical         BOOLEAN NOT NULL DEFAULT true,
  token             TEXT NOT NULL DEFAULT ('sh-' || replace(gen_random_uuid()::text, '-', '')),
  status            TEXT NOT NULL DEFAULT 'pending',
  last_error        TEXT,
  bound_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  verified_at       TIMESTAMPTZ,
  checked_at        TIMESTAMPTZ,
  failing_since     TIMESTAMPTZ,
  lapse_notified_at TIMESTAMPTZ,
  release_at        TIMESTAMPTZ,
  cert_mode         TEXT NOT NULL DEFAULT 'wildcard' CHECK (cert_mode IN ('per_host', 'wildcard')),
  cert_name         TEXT,
  proof_exempt      BOOLEAN NOT NULL DEFAULT false,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS address_families_user_suffix ON address_families (user_id, suffix);
CREATE UNIQUE INDEX IF NOT EXISTS address_families_verified ON address_families (suffix) WHERE verified_at IS NOT NULL;
CREATE TABLE IF NOT EXISTS family_cert_requests (
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  host         TEXT NOT NULL,
  requested_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS family_cert_requests_user ON family_cert_requests (user_id, requested_at);
