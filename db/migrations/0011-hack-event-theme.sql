-- SIM-353: event Look themes. theme selects a whole-page style for the
-- public event page; '' is Classic, today's look, unchanged. The organiser's
-- accent_color keeps driving buttons, links and the icon under every theme.
ALTER TABLE events ADD COLUMN IF NOT EXISTS theme text NOT NULL DEFAULT '';
ALTER TABLE events DROP CONSTRAINT IF EXISTS events_theme_check;
ALTER TABLE events ADD CONSTRAINT events_theme_check
  CHECK (theme IN ('', 'poster', 'confetti', 'boardroom', 'garden', 'terminal'));
