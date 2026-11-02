-- Entry deadline reminders (2026-11-02, entries slice 3). 24 hours and 1 hour
-- before a team's effective deadline, every current member of a team whose
-- entry is not submitted gets one email, through the notices queue
-- (event_notices kind 'entry_reminder'), so the Account opt-out, the
-- key-only rule and the per-kind batching apply unchanged.
-- One row per team per reminder: the dedupe. deadline is the effective
-- deadline the reminder was sent for; when an extension or a deadline change
-- moves it, the reminder may go out once more for the new time.
CREATE TABLE IF NOT EXISTS event_entry_reminders (
  event_id  UUID NOT NULL,
  team_id   UUID NOT NULL REFERENCES event_teams(id) ON DELETE CASCADE,
  kind      TEXT NOT NULL CHECK (kind IN ('24h','1h')),
  deadline  TIMESTAMPTZ NOT NULL,
  sent_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id, team_id, kind)
);
CREATE INDEX IF NOT EXISTS event_entry_reminders_team_idx ON event_entry_reminders (team_id);
ALTER TABLE event_notices DROP CONSTRAINT IF EXISTS event_notices_kind_check;
ALTER TABLE event_notices ADD CONSTRAINT event_notices_kind_check CHECK (kind IN ('join_request','invite',
  'request_answered','application_new','application_decided','site_taken_down','site_restored',
  'deadline_changed','team_admin','team_locked','team_removed','entry_reminder'));
