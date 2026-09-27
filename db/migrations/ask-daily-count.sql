-- "Ask about this page" daily count (2026-09-27).
--
-- POST /v1/ask caps questions per UTC day across everyone (ASK_DAILY_MAX).
-- The count lives here so a restart does not hand out another day's worth.
-- One row per day; nothing about who asked or what.
--
-- Apply once to an existing database, as the table owner, BEFORE deploying the
-- build that reads it (the server refuses to start without this table):
--   sudo -u postgres psql -d simplehost -c 'SET ROLE simplehost' -f db/migrations/ask-daily-count.sql
-- Idempotent; a new empty table, no locks on existing ones.

CREATE TABLE IF NOT EXISTS ask_daily (
  day   DATE PRIMARY KEY,
  count INTEGER NOT NULL DEFAULT 0
);
