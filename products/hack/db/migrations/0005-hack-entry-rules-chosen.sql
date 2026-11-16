-- SIM-382 (owner, 2026-11-05/06): the entry-rules preset picker narrows to
-- two presets (video_or_website, video_website_code), and Overview's first
-- step on a new event becomes "Choose what teams hand in" until the
-- organiser saves a choice.
--
-- entry_rules_chosen tracks whether that choice has ever been saved.
-- DEFAULT true backfills every event that already exists (and every row a
-- fresh install creates from schema.sql) as already chosen -- they already
-- have working rules and never need to see the new first step. The create
-- handler (internal/app/hack/events/create.go) inserts a new event with
-- this column explicitly false; every real entry-rules save
-- (UpdateEventEntryRules, internal/repository/hackevents/judging_settings.go)
-- sets it true for good.
ALTER TABLE events ADD COLUMN IF NOT EXISTS entry_rules_chosen BOOLEAN NOT NULL DEFAULT true;

-- entry_preset's CHECK constraint grows two new values. The three retired
-- presets (build, code, ideas) and custom stay valid stored values -- an
-- event already on one of them keeps working exactly as before -- but
-- PutRules (internal/rules/hackentries/entries.go PresetSettable) refuses
-- to set any of the four again, so only new rows ever carry the new names.
ALTER TABLE events DROP CONSTRAINT IF EXISTS events_entry_preset_check;
ALTER TABLE events ADD CONSTRAINT events_entry_preset_check
  CHECK (entry_preset IN ('build', 'code', 'ideas', 'custom', 'video_or_website', 'video_website_code'));
