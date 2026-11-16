ALTER TABLE event_members ADD COLUMN IF NOT EXISTS team_removal_notice text NOT NULL DEFAULT '';
