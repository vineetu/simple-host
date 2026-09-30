-- simple-hack.app hosted hackathons, milestone 2: team sites, member keys,
-- entries and the submission deadline (docs/designs/simple-hack-platform.md).
-- Used only by an instance running EVENTS=hosted; the server checks these
-- tables at start only in that mode (internal/db/schemacheck.go).
--
-- Additive and idempotent: new columns with constant defaults (catalog-only
-- on Postgres 11+) and new tables.

-- The organiser's entry settings and the public gallery switch.
--   entry_required: which entry fields a complete entry must have, any of
--   title, tagline, description, video_url, code_url, screenshot.
ALTER TABLE events ADD COLUMN IF NOT EXISTS entry_required TEXT[] NOT NULL DEFAULT '{title}';
ALTER TABLE events ADD COLUMN IF NOT EXISTS gallery_open BOOLEAN NOT NULL DEFAULT FALSE;

-- Per team:
--   deadline_override: the organiser gave this team until then instead of
--     events.submission_deadline.
--   pinned_version / pinned_at: the team site's live version when the team's
--     deadline passed (pinned_at set, pinned_version NULL: no site then).
--     Cleared when the deadline moves back into the future.
--   site_taken_down_at: the organiser took the team's site down (the site
--     itself carries sites.suspended_at; this says who did it).
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS deadline_override TIMESTAMPTZ;
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS pinned_version INTEGER;
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMPTZ;
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS site_taken_down_at TIMESTAMPTZ;
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS site_taken_down_reason TEXT NOT NULL DEFAULT '';

-- One entry per team: what judges and the gallery read, kept apart from the
-- site. Plain text and links; the screenshot is a small image checked by its
-- bytes (PNG, JPEG or WebP).
CREATE TABLE IF NOT EXISTS event_entries (
  team_id         UUID PRIMARY KEY REFERENCES event_teams(id) ON DELETE CASCADE,
  event_id        UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  title           TEXT NOT NULL DEFAULT '',
  tagline         TEXT NOT NULL DEFAULT '',
  description     TEXT NOT NULL DEFAULT '',
  video_url       TEXT NOT NULL DEFAULT '',
  code_url        TEXT NOT NULL DEFAULT '',
  screenshot      BYTEA,
  screenshot_type TEXT NOT NULL DEFAULT '',
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by      UUID REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS event_entries_event_idx ON event_entries (event_id);

-- A member's key for their team's site (api_keys.scope = 'team'). The key
-- belongs to the person (api_keys.user_id); it acts on the event's holding
-- account, on one site only, and only while that person is a participant on
-- that team (checked on every request). One per person per event.
CREATE TABLE IF NOT EXISTS event_team_keys (
  key_id     UUID PRIMARY KEY REFERENCES api_keys(id) ON DELETE CASCADE,
  event_id   UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  team_id    UUID NOT NULL REFERENCES event_teams(id) ON DELETE CASCADE,
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (event_id, user_id)
);
CREATE INDEX IF NOT EXISTS event_team_keys_team_idx ON event_team_keys (team_id);
