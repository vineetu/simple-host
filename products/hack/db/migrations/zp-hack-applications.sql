-- Apply to a hackathon (owner, 2026-10-31). Applications on by default, every event.
ALTER TABLE events ADD COLUMN IF NOT EXISTS applications_open boolean NOT NULL DEFAULT true;
-- How a participant arrived, and the optional fields an application asks.
-- Existing members arrived by link; blanks mean "not shared".
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS joined_via text NOT NULL DEFAULT 'link';
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS city text NOT NULL DEFAULT '';
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS country text NOT NULL DEFAULT '';
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS age_range text NOT NULL DEFAULT '';
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS gender text NOT NULL DEFAULT '';
DO $$ BEGIN
  ALTER TABLE event_members ADD CONSTRAINT event_members_joined_via_chk CHECK (joined_via IN ('link','application'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN
  ALTER TABLE event_members ADD CONSTRAINT event_members_age_range_chk CHECK (age_range IN ('','under_18','18_24','25_34','35_plus'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN
  ALTER TABLE event_members ADD CONSTRAINT event_members_gender_chk CHECK (gender IN ('','woman','man','non_binary','another','prefer_not_to_say'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
-- Shape-only: two upper-case ASCII letters. It does not know which codes are
-- real, nameable countries (that excludes AN, CS, SU, UN, the postal-only
-- reservations, ...) or canonical (UK, TP, ...) -- the API's checkHackCountry
-- is the actual gate (security review Grok 9). This check only stops garbage
-- that bypasses the API from a direct insert.
DO $$ BEGIN
  ALTER TABLE event_members ADD CONSTRAINT event_members_country_chk CHECK (country = '' OR country ~ '^[A-Z]{2}$');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- Security review (Grok 1, Grok 8): the per-account and per-event pending
-- caps (too_many_pending, event_applications_full) both count
-- joined_via='application' AND approval_status='pending' rows; these
-- partial indexes are what those counts (and the pending cap's FOR UPDATE
-- lock path) scan instead of the whole table. The per-user one also backs
-- CountPendingApplications; the per-event one backs
-- CountPendingApplicationsForEvent.
CREATE INDEX IF NOT EXISTS event_members_pending_app_user_idx ON event_members (user_id)
  WHERE joined_via = 'application' AND approval_status = 'pending';
CREATE INDEX IF NOT EXISTS event_members_pending_app_event_idx ON event_members (event_id)
  WHERE joined_via = 'application' AND approval_status = 'pending';

-- Grok 8 (migration lock/validation cost): ADD COLUMN ... DEFAULT <constant>
-- is metadata-only on PG11+ (no table rewrite). Each ADD CONSTRAINT CHECK
-- above does take ACCESS EXCLUSIVE and validate every existing row, which is
-- the plain, non-NOT-VALID form. Splitting that into
-- "ADD CONSTRAINT ... NOT VALID" + a later "VALIDATE CONSTRAINT" only saves
-- lock time when the two run in *separate* transactions (VALIDATE takes a
-- weaker lock, but a lock already held earlier in the same transaction is
-- not released early just because a later statement would have settled for
-- less). This file runs as one transaction per db/migrations/migrations.go
-- (the row in schema_migrations, the whole body, and the commit are one
-- unit, so a failure partway leaves nothing applied) -- splitting it would
-- mean an inner COMMIT partway through the file, trading that all-or-nothing
-- guarantee for a lock-time saving that does not matter here anyway: every
-- existing row is already a literal default ('', 'link', true, ...), so
-- validation never has a row to reject and the scan is as fast as a plain
-- read of the table. Keeping one transaction and a plain CHECK is the
-- simpler, equally safe choice.
