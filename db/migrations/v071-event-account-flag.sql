-- Event accounts are marked by a column, not by a key's name (v0.7.1,
-- 2026-09-27, security review L1).
--
-- An account an organiser made for an event gets no sign-in alerts, cannot
-- change its sign-in email and is skipped by the idle cleanup. That used to
-- be read from "holds a key named 'event account'", so any account could
-- name one of its own keys that and get the same treatment. The flag is set
-- by the admin's create and new-key routes only; key names are now plain
-- labels (the reserved ones are refused).
--
-- Additive and idempotent: a column with a constant default (no table
-- rewrite), then a backfill of the accounts that hold such a key today (a
-- handful of rows). Apply before deploying the build that reads it (the
-- server refuses to start without this column).

ALTER TABLE users ADD COLUMN IF NOT EXISTS event_account BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE users u SET event_account = TRUE
 WHERE NOT u.event_account
   AND EXISTS (SELECT 1 FROM api_keys k WHERE k.user_id = u.id AND k.name = 'event account');
