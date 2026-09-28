-- The setup helper's assistant daily count.
--
-- POST /v1/setup/assist caps messages per UTC day across everyone
-- (SETUP_ASSIST_DAILY_MAX). The count lives here so a restart does not hand
-- out another day's worth. One row per day; nothing about who asked or what.
--
-- Additive and idempotent: a new empty table, no locks on existing ones.
-- Apply before deploying the build that reads it (the server refuses to start
-- without this table).

CREATE TABLE IF NOT EXISTS setup_assist_daily (
  day   DATE PRIMARY KEY,
  count INTEGER NOT NULL DEFAULT 0
);
