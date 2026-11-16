-- Judge notices, custom reminder offsets and organiser team messages
-- (2026-11-05, .notices-contract.md). Everything rides the existing
-- event_notices table and email queue: no parallel system.

-- One list drives both audiences: entry reminders to participants (offsets
-- before a team's deadline) and judging reminders to judges (offsets before
-- judging_ends_at). Minutes before the deadline; 0..5 entries, each
-- 15..20160 (14 days), distinct, stored sorted descending. Empty = no
-- reminders.
ALTER TABLE events ADD COLUMN IF NOT EXISTS reminder_offsets INTEGER[] NOT NULL DEFAULT '{1440,60}';

-- When judging closes. Optional: organisers set it like the other event
-- times (PATCH .../events/{slug}). NULL means judges get no reminder, and
-- the schedule shows no fixed "Judging closes" row.
ALTER TABLE events ADD COLUMN IF NOT EXISTS judging_ends_at TIMESTAMPTZ;

-- The old fixed ('24h','1h') pair becomes any offset label, so a custom
-- reminder offset (e.g. 4320 minutes -> '72h') dedupes the same way.
ALTER TABLE event_entry_reminders DROP CONSTRAINT IF EXISTS event_entry_reminders_kind_check;
ALTER TABLE event_entry_reminders ADD CONSTRAINT event_entry_reminders_kind_check CHECK (kind ~ '^[0-9]+[hm]$');

-- Judging-close reminders: one row per (judge, offset), re-armed when
-- judging_ends_at moves (the deadline column changes under the same kind).
CREATE TABLE IF NOT EXISTS event_judge_reminders (
  event_id  UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind      TEXT NOT NULL CHECK (kind ~ '^[0-9]+[hm]$'),
  deadline  TIMESTAMPTZ NOT NULL,
  sent_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id, user_id, kind)
);

-- Organiser team messages: the durable history (notices are pruned after 30
-- days and exist only per recipient; delivery is the notice row). One row
-- per team per send; sender_name is captured at send time so the history
-- reads correctly even after the sender's account or display name changes.
CREATE TABLE IF NOT EXISTS event_team_messages (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id    UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  team_id     UUID NOT NULL REFERENCES event_teams(id) ON DELETE CASCADE,
  sender_id   UUID REFERENCES users(id) ON DELETE SET NULL,
  sender_name TEXT NOT NULL DEFAULT '',
  body        TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS event_team_messages_team_idx ON event_team_messages (team_id, created_at DESC);
CREATE INDEX IF NOT EXISTS event_team_messages_event_idx ON event_team_messages (event_id, created_at);

ALTER TABLE event_notices DROP CONSTRAINT IF EXISTS event_notices_kind_check;
ALTER TABLE event_notices ADD CONSTRAINT event_notices_kind_check CHECK (kind IN ('join_request','invite',
  'request_answered','application_new','application_decided','site_taken_down','site_restored',
  'deadline_changed','team_admin','team_locked','team_removed','entry_reminder',
  'judging_open','judging_reminder','team_message'));
