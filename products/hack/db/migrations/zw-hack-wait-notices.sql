-- Notify people when something waits on them (2026-11-02, audit item 7).
-- One row per person per thing that happened: a join request for a team's
-- admins, an invite, an application for the organisers, a decision, a site
-- take-down or restore, a deadline change, being made team admin, a team
-- lock, a removal. The row is both the in-app news line (seen_at) and the
-- email queue (email_state). team_id and event_id have no foreign key on
-- purpose: a removal notice outlives the team it names, and an insert, often
-- made while member or team rows are already locked, must not take a
-- key-share lock on an event or team row out of the event, member, team lock
-- order (a stage change holds the event FOR UPDATE and then waits on those
-- rows). DeleteEvent removes an event's notices itself, and the email sweep
-- skips and prunes any whose event is gone.
CREATE TABLE IF NOT EXISTS event_notices (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id     UUID NOT NULL,
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL CHECK (kind IN ('join_request','invite','request_answered','application_new',
                 'application_decided','site_taken_down','site_restored','deadline_changed','team_admin',
                 'team_locked','team_removed')),
  team_id      UUID,
  ref_id       UUID,
  detail       TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  seen_at      TIMESTAMPTZ,
  -- none: never emailed (no address, opted out, or an in-app-only kind);
  -- pending: waits for the sweep; sent; skipped: stale or opted out at send time.
  email_state  TEXT NOT NULL DEFAULT 'none' CHECK (email_state IN ('none','pending','sent','skipped')),
  emailed_at   TIMESTAMPTZ,
  attempted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS event_notices_user_event_idx ON event_notices (user_id, event_id, created_at DESC);
CREATE INDEX IF NOT EXISTS event_notices_pending_idx ON event_notices (created_at) WHERE email_state = 'pending';
CREATE INDEX IF NOT EXISTS event_notices_sent_idx ON event_notices (user_id, kind, emailed_at) WHERE email_state = 'sent';
CREATE INDEX IF NOT EXISTS event_notices_team_idx ON event_notices (team_id) WHERE team_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS event_notices_created_idx ON event_notices (created_at);
CREATE INDEX IF NOT EXISTS event_notices_event_idx ON event_notices (event_id);
-- A database that ran an earlier draft of this file kept the foreign key.
ALTER TABLE event_notices DROP CONSTRAINT IF EXISTS event_notices_event_id_fkey;
-- "Email me when something waits on me" on Account. On unless turned off.
ALTER TABLE hack_account_preferences ADD COLUMN IF NOT EXISTS email_updates BOOLEAN NOT NULL DEFAULT TRUE;
