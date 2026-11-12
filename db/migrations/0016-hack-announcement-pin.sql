-- One pinned announcement per event (SIM-444). Null = not pinned.
ALTER TABLE event_announcements ADD COLUMN IF NOT EXISTS pinned_at timestamptz;
CREATE UNIQUE INDEX IF NOT EXISTS event_announcements_pinned_idx ON event_announcements (event_id) WHERE pinned_at IS NOT NULL;
