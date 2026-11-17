-- Company sign-in (OIDC), 2026-10-16: a third provider name, "oidc", joins
-- "google" and "github" in the two check constraints that otherwise refuse
-- it outright (oauth_identities.provider and oauth_states' own check). Both
-- tables are small (per-linked-identity and short-lived in-flight sign-ins),
-- so re-validating the constraint is cheap even on a live database.
ALTER TABLE oauth_identities DROP CONSTRAINT IF EXISTS oauth_identities_provider_check;
ALTER TABLE oauth_identities
  ADD CONSTRAINT oauth_identities_provider_check
  CHECK (provider IN ('google', 'github', 'oidc'));

ALTER TABLE oauth_states DROP CONSTRAINT IF EXISTS oauth_states_provider_check;
ALTER TABLE oauth_states
  ADD CONSTRAINT oauth_states_provider_check
  CHECK (provider IN ('google', 'github', 'oidc'));
