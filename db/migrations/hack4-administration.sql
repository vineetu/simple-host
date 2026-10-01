-- One expiring, single-use co-organiser invitation per event. Only its hash is stored.
CREATE TABLE IF NOT EXISTS event_organiser_invites (
  event_id UUID PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL
);
