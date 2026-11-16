ALTER TABLE events ADD COLUMN IF NOT EXISTS overall_prizes text[] NOT NULL DEFAULT '{}';
