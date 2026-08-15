-- hash-session-tokens.sql
--
-- MUST be applied as the app role (the role the binary uses, e.g. shdev),
-- NOT as postgres. Applying as postgres leaves tables owned by the wrong
-- role and the app then gets permission denied.
--
-- Production has 0 rows in visitor_sessions, visitor_establish_tokens, and
-- oauth_states. There is no data to migrate and no dual-read path. The
-- database now stores only sha256 of presented tokens; the raw values
-- (cookie, establish `once`, OAuth `state`) are never written.

-- The session cookie token is generated at establish time, so this row
-- no longer points at a session id (which would have been the cookie).
DROP TABLE IF EXISTS visitor_establish_tokens;

CREATE TABLE visitor_establish_tokens (
  once         TEXT PRIMARY KEY, -- sha256 hex of the URL `once` value
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  site_id      UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  host         TEXT NOT NULL,
  return_to    TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL,
  used_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS visitor_establish_tokens_expires_idx
  ON visitor_establish_tokens (expires_at);

COMMENT ON COLUMN visitor_sessions.id IS
  'SHA-256 of the session cookie token. The raw token is never stored.';
COMMENT ON COLUMN visitor_establish_tokens.once IS
  'SHA-256 (hex) of the one-time establish URL token. The raw token is never stored.';
COMMENT ON COLUMN oauth_states.state IS
  'SHA-256 (hex) of the OAuth state parameter. The raw token is never stored.';
