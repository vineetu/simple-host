-- 2026-09-24: the Simple Host connector. A remote MCP endpoint (/mcp) behind
-- an OAuth 2.1 authorization server built into this binary, so a person adds
-- Simple Host once in ChatGPT, Claude or Grok, signs in once, and every later
-- chat is already signed in. See INTENT.md, decision of 2026-09-24.
--
-- APPLY AS THE ROLE IN DB_DSN (the role that owns the existing tables), NOT as
-- a superuser, or the application role gets "permission denied" at runtime:
--   psql "$DB_DSN" -f db/migrations/oauth-connector.sql
-- Additive and safe to re-run. Safe to apply before deploying the binary that
-- uses it: nothing older reads these tables.
--
-- Every secret here (client secrets, codes, tokens) is stored as a SHA-256 hex
-- digest of a 256-bit random value, never in the clear. Rows hang off users
-- with ON DELETE CASCADE, so deleting a person removes every app they
-- connected and every token those apps hold.

-- Apps that registered themselves (RFC 7591) or were inserted by the operator.
CREATE TABLE IF NOT EXISTS oauth_clients (
  client_id                  TEXT PRIMARY KEY,
  client_secret_hash         TEXT,
  client_name                TEXT NOT NULL,
  redirect_uris              TEXT[] NOT NULL,
  token_endpoint_auth_method TEXT NOT NULL DEFAULT 'none',
  -- FALSE only for an operator-registered confidential client whose platform
  -- cannot send PKCE (ChatGPT GPT Actions). Every self-registered client and
  -- every public client must use PKCE S256.
  pkce_required              BOOLEAN NOT NULL DEFAULT TRUE,
  -- TRUE for RFC 7591 self-registration; FALSE for clients created by the
  -- operator (`simple-host oauth-client create`), which the sweep never removes.
  dynamic                    BOOLEAN NOT NULL DEFAULT TRUE,
  created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at               TIMESTAMPTZ,
  CONSTRAINT oauth_clients_auth_method_check
    CHECK (token_endpoint_auth_method IN ('none', 'client_secret_post', 'client_secret_basic')),
  CONSTRAINT oauth_clients_secret_shape
    CHECK ((token_endpoint_auth_method = 'none') = (client_secret_hash IS NULL)),
  CONSTRAINT oauth_clients_pkce_check
    CHECK (pkce_required OR (client_secret_hash IS NOT NULL AND NOT dynamic))
);
CREATE INDEX IF NOT EXISTS oauth_clients_created_idx ON oauth_clients (created_at);

-- One grant per "Allow": a person connected an app. It is the refresh-token
-- family: rotating a refresh token stays inside it, and reuse of a rotated
-- token deletes it (and with it every token it issued). Disconnecting an app
-- deletes its grants.
CREATE TABLE IF NOT EXISTS oauth_grants (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  client_id    TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
  scope        TEXT NOT NULL,
  resource     TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS oauth_grants_user_idx ON oauth_grants (user_id, client_id);
CREATE INDEX IF NOT EXISTS oauth_grants_client_idx ON oauth_grants (client_id);

-- Authorization codes: single use, ~60 seconds, bound to the client, the
-- redirect URI, the PKCE challenge and the resource. grant_id is set when the
-- code is redeemed, so a second redemption can revoke what the first issued.
CREATE TABLE IF NOT EXISTS oauth_codes (
  code_hash      TEXT PRIMARY KEY,
  client_id      TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
  user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  redirect_uri   TEXT NOT NULL,
  code_challenge TEXT NOT NULL,
  scope          TEXT NOT NULL,
  resource       TEXT NOT NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at     TIMESTAMPTZ NOT NULL,
  used_at        TIMESTAMPTZ,
  grant_id       UUID REFERENCES oauth_grants(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS oauth_codes_expires_idx ON oauth_codes (expires_at);

-- Access tokens (~1 hour) and refresh tokens (rotating, long-lived).
-- used_at on a refresh token marks it rotated; presenting it again is reuse.
CREATE TABLE IF NOT EXISTS oauth_tokens (
  token_hash TEXT PRIMARY KEY,
  grant_id   UUID NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  used_at    TIMESTAMPTZ,
  CONSTRAINT oauth_tokens_kind_check CHECK (kind IN ('access', 'refresh'))
);
CREATE INDEX IF NOT EXISTS oauth_tokens_grant_idx ON oauth_tokens (grant_id);
CREATE INDEX IF NOT EXISTS oauth_tokens_expires_idx ON oauth_tokens (expires_at);
