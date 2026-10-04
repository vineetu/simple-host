-- Optional organiser choice; empty keeps the deterministic slug colour.
ALTER TABLE events ADD COLUMN IF NOT EXISTS accent_color TEXT NOT NULL DEFAULT '';
