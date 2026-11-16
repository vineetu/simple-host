-- P1 registration and tracks. Existing members stay approved.
ALTER TABLE events ADD COLUMN IF NOT EXISTS signup_questions JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE events ADD COLUMN IF NOT EXISTS approval_required BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS signup_answers JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS approval_status TEXT NOT NULL DEFAULT 'approved';
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS approval_decided_at TIMESTAMPTZ;
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS approval_decided_by UUID REFERENCES users(id) ON DELETE SET NULL;
CREATE TABLE IF NOT EXISTS event_tracks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  slug TEXT NOT NULL,
  name TEXT NOT NULL,
  challenge TEXT NOT NULL DEFAULT '',
  prize TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (event_id, slug)
);
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS track_id UUID REFERENCES event_tracks(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS event_teams_track_idx ON event_teams (track_id);
