-- The event page may use an organiser-published site. Existing events keep
-- their built-in page until an organiser publishes a custom version.
ALTER TABLE events ADD COLUMN IF NOT EXISTS website_mode TEXT NOT NULL DEFAULT 'builtin';
DO $$ BEGIN
  ALTER TABLE events ADD CONSTRAINT events_website_mode_check
    CHECK (website_mode IN ('builtin', 'custom'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
