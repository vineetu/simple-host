ALTER TABLE events ADD COLUMN IF NOT EXISTS team_finding boolean NOT NULL DEFAULT true;
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS open boolean NOT NULL DEFAULT true;
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS looking_for text NOT NULL DEFAULT '';
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS looking_intro text NOT NULL DEFAULT '';
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS looking boolean NOT NULL DEFAULT false;
CREATE TABLE IF NOT EXISTS event_team_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  team_id uuid NOT NULL REFERENCES event_teams(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('request','invite')),
  created_by uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (team_id, user_id)
);
