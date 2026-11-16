-- Event icon for public pages, lists, and metadata. Existing events have no icon.
ALTER TABLE events ADD COLUMN IF NOT EXISTS icon_media_type TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN IF NOT EXISTS icon_bytes BYTEA;
