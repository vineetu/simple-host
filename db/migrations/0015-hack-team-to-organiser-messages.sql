-- Teams can write to the organisers (SIM-443). The same event_team_messages
-- thread (one per team, shared by all organisers) now holds both directions;
-- from_team marks a message a team member wrote. Organisers are told through
-- a new event_notices kind, team_inbound, on the same notice and email queue.
ALTER TABLE event_team_messages ADD COLUMN IF NOT EXISTS from_team BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE event_notices DROP CONSTRAINT IF EXISTS event_notices_kind_check;
ALTER TABLE event_notices ADD CONSTRAINT event_notices_kind_check CHECK (kind IN ('join_request','invite',
  'request_answered','application_new','application_decided','site_taken_down','site_restored',
  'deadline_changed','team_admin','team_locked','team_removed','entry_reminder',
  'judging_open','judging_reminder','team_message','team_inbound'));
