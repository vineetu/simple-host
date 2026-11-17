-- Per-person addresses (owner decision 2026-09-25): an account's handle is also
-- its address, <handle>.simple-host.app. When the operator renames a handle, the
-- old one is kept here so links naming it keep resolving (API routes, the old
-- person address), and so nobody else can take it.
--
-- Apply once to an existing database, as the table owner, BEFORE deploying the
-- build that reads it (the server refuses to start without this table):
--   sudo -u postgres psql -d simplehost -c 'SET ROLE simplehost' -f db/migrations/handle-aliases.sql
-- Idempotent.

CREATE TABLE IF NOT EXISTS handle_aliases (
  handle     TEXT PRIMARY KEY,
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
