ALTER TABLE events ADD COLUMN IF NOT EXISTS submissions_public boolean NOT NULL DEFAULT false;
