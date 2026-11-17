-- w3 hosted (2026-09-27): tell apart connections of the same app. The consent
-- page's browser, summarised ("Chrome on macOS"), is kept on the code and
-- copied onto the grant; the Connected apps list shows it. Never the user
-- agent itself and never an IP. Connections made before this show none.
--
-- Apply BEFORE deploying the build that reads it (the server refuses to start
-- without it). Idempotent; nullable columns with no default are catalog-only.
ALTER TABLE oauth_codes ADD COLUMN IF NOT EXISTS device TEXT;
ALTER TABLE oauth_grants ADD COLUMN IF NOT EXISTS device TEXT;
