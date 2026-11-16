-- People's choice and public event directory. Hosted Hack only; empty elsewhere.
ALTER TABLE events ADD COLUMN IF NOT EXISTS voting_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE events ADD COLUMN IF NOT EXISTS voting_opens_at TIMESTAMPTZ;
ALTER TABLE events ADD COLUMN IF NOT EXISTS voting_closes_at TIMESTAMPTZ;
ALTER TABLE events ADD COLUMN IF NOT EXISTS voting_eligibility TEXT NOT NULL DEFAULT 'all_signed_in';
ALTER TABLE events ADD COLUMN IF NOT EXISTS directory_listed BOOLEAN NOT NULL DEFAULT TRUE;

CREATE TABLE IF NOT EXISTS event_votes (
  event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  voter_email TEXT NOT NULL,
  team_id UUID NOT NULL REFERENCES event_teams(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id, voter_email)
);
CREATE INDEX IF NOT EXISTS event_votes_team_idx ON event_votes (event_id, team_id);
CREATE INDEX IF NOT EXISTS events_directory_idx ON events (starts_at, slug) WHERE directory_listed AND stage <> 'draft' AND taken_down_at IS NULL;
