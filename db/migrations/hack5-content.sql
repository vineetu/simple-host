-- Event-page content and durable participant mail for hosted Simple Hack.
CREATE TABLE IF NOT EXISTS event_content (
  event_id UUID PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
  sponsors JSONB NOT NULL DEFAULT '[]'::jsonb,
  faq JSONB NOT NULL DEFAULT '[]'::jsonb,
  schedule JSONB NOT NULL DEFAULT '[]'::jsonb,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS event_announcements (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS event_announcements_event_idx ON event_announcements (event_id, created_at DESC);

CREATE TABLE IF NOT EXISTS event_announcement_deliveries (
  announcement_id UUID NOT NULL REFERENCES event_announcements(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  recipient TEXT NOT NULL,
  sent_at TIMESTAMPTZ,
  PRIMARY KEY (announcement_id, user_id)
);
CREATE INDEX IF NOT EXISTS event_announcement_deliveries_pending_idx
  ON event_announcement_deliveries (announcement_id, user_id) WHERE sent_at IS NULL;

CREATE TABLE IF NOT EXISTS event_entry_receipts (
  event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  team_id UUID NOT NULL REFERENCES event_teams(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  recipient TEXT NOT NULL,
  sent_at TIMESTAMPTZ,
  PRIMARY KEY (event_id, team_id)
);
CREATE INDEX IF NOT EXISTS event_entry_receipts_pending_idx
  ON event_entry_receipts (event_id, team_id) WHERE sent_at IS NULL;
