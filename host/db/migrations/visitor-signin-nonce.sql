-- Visitor Google sign-in is bound to the browser that started it (login CSRF
-- fix, 2026-09-26). The start hop on the site's own host sets a short-lived
-- __Host- nonce cookie there; only its SHA-256 (unpadded base64url) travels
-- through the OAuth state to the one-time establish code, and establish
-- refuses (and burns) a code whose browser does not hold the nonce.
--
-- Apply once to an existing database, as the table owner, BEFORE deploying the
-- build that reads it (the server refuses to start without these columns):
--   sudo -u postgres psql -d simplehost -c 'SET ROLE simplehost' -f db/migrations/visitor-signin-nonce.sql
-- Idempotent. In-flight rows keep NULL: such a sign-in is refused at the
-- callback (and an unbound establish code at establish).

ALTER TABLE oauth_states
  ADD COLUMN IF NOT EXISTS nonce_hash TEXT;

ALTER TABLE visitor_establish_tokens
  ADD COLUMN IF NOT EXISTS nonce_hash TEXT;
