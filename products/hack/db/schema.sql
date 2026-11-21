-- Hack schema: load after host/db/schema.sql.

-- simple-hack.app hosted hackathons (EVENTS=hosted; docs/designs/simple-hack-platform.md,
-- products/hack/db/migrations/hack1-events.sql).
CREATE TABLE IF NOT EXISTS events (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slug                  TEXT NOT NULL UNIQUE,
  account_id            UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE RESTRICT,
  created_by            UUID REFERENCES users(id) ON DELETE SET NULL,
  title                 TEXT NOT NULL,
  stage                 TEXT NOT NULL DEFAULT 'draft'
                        CHECK (stage IN ('draft','open','building','closed','judging','results','archived')),
  -- Who is running it, collected at creation and shown to the platform admin.
  organiser_name        TEXT NOT NULL,
  organisation          TEXT NOT NULL DEFAULT '',
  contact_email         TEXT NOT NULL,
  purpose               TEXT NOT NULL DEFAULT '',
  expected_participants INTEGER CHECK (expected_participants IS NULL OR expected_participants >= 0),
  -- The public page. Plain text; the server escapes it and never runs it.
  tagline               TEXT NOT NULL DEFAULT '',
  website_mode          TEXT NOT NULL DEFAULT 'builtin' CHECK (website_mode IN ('builtin', 'custom')),
  icon_media_type       TEXT NOT NULL DEFAULT '',
  accent_color          TEXT NOT NULL DEFAULT '',
  icon_bytes            BYTEA,
  about                 TEXT NOT NULL DEFAULT '',
  rules                 TEXT NOT NULL DEFAULT '',
  prizes                TEXT NOT NULL DEFAULT '',
  submissions_public boolean NOT NULL DEFAULT false,
  overall_prizes        TEXT[] NOT NULL DEFAULT '{}',
  coc_text              TEXT NOT NULL DEFAULT '',
  time_zone             TEXT NOT NULL DEFAULT 'UTC',
  starts_at             TIMESTAMPTZ,
  ends_at               TIMESTAMPTZ,
  -- Settings.
  team_size_max         INTEGER NOT NULL DEFAULT 20 CHECK (team_size_max BETWEEN 1 AND 50),
  join_code             TEXT NOT NULL UNIQUE,
  judge_code            TEXT NOT NULL UNIQUE,
  -- M2: entries freeze at this time.
  submission_deadline   TIMESTAMPTZ,
  -- M3: the public sees winners only, or the full ranking. Each team always
  -- sees its own scores and the judges' comments privately.
  results_visibility    TEXT NOT NULL DEFAULT 'winners' CHECK (results_visibility IN ('winners','ranking')),
  results_published_at  TIMESTAMPTZ,
  -- M4: when the event closed (stage archived). Team sites are removed
  -- EVENT_SITES_KEEP_DAYS later, after warning emails; the event page and
  -- results stay.
  closed_at             TIMESTAMPTZ,
  removal_warned_at     TIMESTAMPTZ,
  sites_removed_at      TIMESTAMPTZ,
  keep_sites            BOOLEAN NOT NULL DEFAULT FALSE,
  -- The platform admin's take-down.
  taken_down_at         TIMESTAMPTZ,
  taken_down_reason     TEXT NOT NULL DEFAULT '',
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- 0003-hack-notices-judges-messages.sql: minutes before a deadline, 0..5
  -- entries each 15..20160, distinct, sorted descending. Drives both entry
  -- reminders (participants) and judging reminders (judges). judging_ends_at
  -- is optional, set like the event's other times; NULL means judges get no
  -- reminder.
  reminder_offsets      INTEGER[] NOT NULL DEFAULT '{1440,60}',
  judging_ends_at       TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS events_created_by_idx ON events (created_by, created_at);

CREATE TABLE IF NOT EXISTS event_teams (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id    UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  -- One DNS label: <slug>.<event>.<SITE_DOMAIN> and, from M2, the site name.
  slug        TEXT NOT NULL,
  name        TEXT NOT NULL,
  code        TEXT NOT NULL UNIQUE,
  created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (event_id, slug)
);

-- Team names are unique within an event, whatever their case.
CREATE UNIQUE INDEX IF NOT EXISTS event_teams_name_idx ON event_teams (event_id, lower(name));

-- One row per person per event: one role each (an organiser is not also a
-- participant, a judge is never on a team). A participant is on at most one
-- team of the event (team_id).
CREATE TABLE IF NOT EXISTS event_members (
    team_removal_notice text NOT NULL DEFAULT '',
  event_id        UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role            TEXT NOT NULL CHECK (role IN ('organiser','participant','judge')),
  display_name    TEXT NOT NULL DEFAULT '', -- unused since 2026-11-06 (one name per person: users.display_name)
  team_id         UUID REFERENCES event_teams(id) ON DELETE SET NULL,
  coc_accepted_at TIMESTAMPTZ,
  joined_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id, user_id),
  CHECK (team_id IS NULL OR role = 'participant')
);
CREATE INDEX IF NOT EXISTS event_members_user_idx ON event_members (user_id);
CREATE INDEX IF NOT EXISTS event_members_team_idx ON event_members (team_id);

-- Historical and unused event creation log. The daily limit counts existing events.
CREATE TABLE IF NOT EXISTS event_create_log (
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS event_create_log_user_idx ON event_create_log (user_id, created_at);

-- simple-hack.app M2: team sites, member keys, entries, the submission deadline
-- (host/db/migrations/hack2-team-sites.sql).

-- The organiser's entry settings and the public gallery switch.
--   entry_required: which entry fields a complete entry must have, any of
--   title, tagline, description, video_url, code_url, screenshot.
ALTER TABLE events ADD COLUMN IF NOT EXISTS entry_required TEXT[] NOT NULL DEFAULT '{title}';
ALTER TABLE events ADD COLUMN IF NOT EXISTS gallery_open BOOLEAN NOT NULL DEFAULT FALSE;

-- Per team:
--   deadline_override: the organiser gave this team until then instead of
--     events.submission_deadline.
--   pinned_version / pinned_at: the team site's live version when the team's
--     deadline passed (pinned_at set, pinned_version NULL: no site then).
--     Cleared when the deadline moves back into the future.
--   site_taken_down_at: the organiser took the team's site down (the site
--     itself carries sites.suspended_at; this says who did it).
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS deadline_override TIMESTAMPTZ;
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS pinned_version INTEGER;
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMPTZ;
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS site_taken_down_at TIMESTAMPTZ;
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS site_taken_down_reason TEXT NOT NULL DEFAULT '';

-- One entry per team: what judges and the gallery read, kept apart from the
-- site. Plain text and links; the screenshot is a small image checked by its
-- bytes (PNG, JPEG or WebP).
CREATE TABLE IF NOT EXISTS event_entries (
  team_id         UUID PRIMARY KEY REFERENCES event_teams(id) ON DELETE CASCADE,
  event_id        UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  title           TEXT NOT NULL DEFAULT '',
  tagline         TEXT NOT NULL DEFAULT '',
  description     TEXT NOT NULL DEFAULT '',
  video_url       TEXT NOT NULL DEFAULT '',
  code_url        TEXT NOT NULL DEFAULT '',
  screenshot      BYTEA,
  screenshot_type TEXT NOT NULL DEFAULT '',
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by      UUID REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS event_entries_event_idx ON event_entries (event_id);

-- Public event-page content and queued participant email.
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
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  pinned_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS event_announcements_event_idx ON event_announcements (event_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS event_announcements_pinned_idx ON event_announcements (event_id) WHERE pinned_at IS NOT NULL;
CREATE TABLE IF NOT EXISTS event_announcement_deliveries (
  announcement_id UUID NOT NULL REFERENCES event_announcements(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  recipient TEXT NOT NULL,
  sent_at TIMESTAMPTZ,
  attempted_at TIMESTAMPTZ,
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
  attempted_at TIMESTAMPTZ,
  PRIMARY KEY (event_id, team_id)
);
CREATE INDEX IF NOT EXISTS event_entry_receipts_pending_idx
  ON event_entry_receipts (event_id, team_id) WHERE sent_at IS NULL;

-- Every event name and team name that ever had a team, kept when the event or
-- team is deleted: a browser keeps an origin's service workers, storage and
-- cookies, so <team>.<event>.<SITE_DOMAIN> never passes to anyone else. No
-- foreign keys, on purpose. team_slug '' marks the event name itself.
CREATE TABLE IF NOT EXISTS event_used_names (
  event_slug TEXT NOT NULL,
  team_slug  TEXT NOT NULL DEFAULT '',
  used_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (event_slug, team_slug)
);

-- Names already in use when this runs (events made before M2) are kept too.
INSERT INTO event_used_names (event_slug, team_slug)
SELECT e.slug, '' FROM events e WHERE EXISTS (SELECT 1 FROM event_teams t WHERE t.event_id = e.id)
UNION
SELECT e.slug, t.slug FROM events e JOIN event_teams t ON t.event_id = e.id
ON CONFLICT DO NOTHING;

-- simple-hack.app M3: judging (host/db/migrations/hack3-judging.sql).
-- Weights sum to 100 in Go, not here. A conflict removes that judge from
-- totals even when score rows remain. event_results.snapshot is the ranking
-- at publish time; full_ranking only changes what the public results route shows.
ALTER TABLE events ADD COLUMN IF NOT EXISTS organiser_judges uuid[] NOT NULL DEFAULT '{}'::uuid[];
ALTER TABLE events ADD COLUMN IF NOT EXISTS judge_assignment_mode TEXT NOT NULL DEFAULT 'open';
ALTER TABLE events ADD COLUMN IF NOT EXISTS judges_per_team INTEGER NOT NULL DEFAULT 2;
ALTER TABLE events ADD COLUMN IF NOT EXISTS judging_locked_at TIMESTAMPTZ;
ALTER TABLE events ADD COLUMN IF NOT EXISTS judging_lock_reason TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS rubric_criteria (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id    UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  position    INTEGER NOT NULL,
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  weight      INTEGER NOT NULL,
  max_points  INTEGER NOT NULL DEFAULT 5,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS rubric_criteria_event_idx ON rubric_criteria (event_id, position);

CREATE TABLE IF NOT EXISTS event_assignments (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id   UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  judge_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  team_id    UUID NOT NULL REFERENCES event_teams(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (event_id, judge_id, team_id)
);
CREATE INDEX IF NOT EXISTS event_assignments_judge_idx ON event_assignments (event_id, judge_id);
CREATE INDEX IF NOT EXISTS event_assignments_team_idx ON event_assignments (team_id);

CREATE TABLE IF NOT EXISTS event_conflicts (
  event_id     UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  judge_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  team_id      UUID NOT NULL REFERENCES event_teams(id) ON DELETE CASCADE,
  declared_by  TEXT NOT NULL DEFAULT 'judge',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id, judge_id, team_id)
);

CREATE TABLE IF NOT EXISTS event_scores (
  event_id     UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  judge_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  team_id      UUID NOT NULL REFERENCES event_teams(id) ON DELETE CASCADE,
  criterion_id UUID NOT NULL REFERENCES rubric_criteria(id) ON DELETE CASCADE,
  points       INTEGER NOT NULL,
  comment      TEXT NOT NULL DEFAULT '',
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (judge_id, team_id, criterion_id)
);
CREATE INDEX IF NOT EXISTS event_scores_event_idx ON event_scores (event_id);
CREATE INDEX IF NOT EXISTS event_scores_team_idx ON event_scores (team_id);

CREATE TABLE IF NOT EXISTS event_results (
  event_id      UUID PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
  published_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  full_ranking  BOOLEAN NOT NULL DEFAULT FALSE,
  snapshot      JSONB NOT NULL
);

-- One expiring, single-use co-organiser invitation per event. Only its hash is stored.
CREATE TABLE IF NOT EXISTS event_organiser_invites (
  event_id UUID PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL
);
-- P1 registration and tracks. Existing members stay approved.
ALTER TABLE events ADD COLUMN IF NOT EXISTS signup_questions JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE events ADD COLUMN IF NOT EXISTS approval_required BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS signup_answers JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS approval_status TEXT NOT NULL DEFAULT 'approved';
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS approval_decided_at TIMESTAMPTZ;
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS approval_decided_by UUID REFERENCES users(id) ON DELETE SET NULL;
CREATE TABLE IF NOT EXISTS event_tracks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  slug TEXT NOT NULL,
  name TEXT NOT NULL,
  challenge TEXT NOT NULL DEFAULT '',
  prize TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (event_id, slug)
);
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS track_id UUID REFERENCES event_tracks(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS event_teams_track_idx ON event_teams (track_id);

-- simple-hack.app P1: a changeable people's-choice vote per account (voter_id,
-- 0029-hack-votes-by-account.sql);
-- public event directory is listed by default (hack7-voting-directory.sql).
ALTER TABLE events ADD COLUMN IF NOT EXISTS voting_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE events ADD COLUMN IF NOT EXISTS voting_opens_at TIMESTAMPTZ;
ALTER TABLE events ADD COLUMN IF NOT EXISTS voting_closes_at TIMESTAMPTZ;
ALTER TABLE events ADD COLUMN IF NOT EXISTS voting_eligibility TEXT NOT NULL DEFAULT 'all_signed_in';
ALTER TABLE events ADD COLUMN IF NOT EXISTS directory_listed BOOLEAN NOT NULL DEFAULT FALSE;
CREATE TABLE IF NOT EXISTS event_votes (
  event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  voter_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  voter_email TEXT, -- unused since 2026-11-21 (votes are per voter_id)
  team_id UUID NOT NULL REFERENCES event_teams(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id, voter_id)
);
CREATE INDEX IF NOT EXISTS event_votes_team_idx ON event_votes (event_id, team_id);
CREATE INDEX IF NOT EXISTS events_directory_idx ON events (starts_at, slug) WHERE directory_listed AND stage <> 'draft' AND taken_down_at IS NULL;

-- Hosted judging options (hack8-judging-options.sql).
ALTER TABLE events ADD COLUMN IF NOT EXISTS score_mode TEXT NOT NULL DEFAULT 'raw';
ALTER TABLE events ADD COLUMN IF NOT EXISTS tie_criterion_id UUID REFERENCES rubric_criteria(id) ON DELETE SET NULL;
ALTER TABLE events ADD COLUMN IF NOT EXISTS public_scores BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE events ADD COLUMN IF NOT EXISTS public_ranks BOOLEAN NOT NULL DEFAULT TRUE;
CREATE TABLE IF NOT EXISTS event_track_panels (
  event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  track_id UUID NOT NULL REFERENCES event_tracks(id) ON DELETE CASCADE,
  judge_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (event_id, track_id, judge_id)
);
CREATE INDEX IF NOT EXISTS event_track_panels_judge_idx ON event_track_panels(event_id, judge_id);

-- Hosted-event account preferences; ordinary Simple Host accounts never use it.
CREATE TABLE IF NOT EXISTS hack_account_preferences (
  user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  theme TEXT NOT NULL DEFAULT 'system' CHECK (theme IN ('system','light','dark')),
  organiser_walkthrough_done BOOLEAN NOT NULL DEFAULT FALSE,
  judge_walkthrough_done BOOLEAN NOT NULL DEFAULT FALSE
);
-- A subscription belongs to the person, not the event's holding account.
-- Delete it on membership loss, including moves and approval revocation.
CREATE OR REPLACE FUNCTION revoke_hack_notifications() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'UPDATE' THEN
  IF OLD.team_id IS NOT DISTINCT FROM NEW.team_id AND OLD.role = NEW.role
     AND OLD.approval_status = NEW.approval_status THEN
   RETURN NEW;
  END IF;
 END IF;
 DELETE FROM site_notifications n USING sites s, events e
 WHERE n.site_id=s.id AND n.owner_id=OLD.user_id AND e.id=OLD.event_id
 AND s.user_id=e.account_id;
 RETURN NULL;
END;
$$;
CREATE OR REPLACE TRIGGER hack_members_notifications
 AFTER UPDATE OR DELETE ON event_members
 FOR EACH ROW EXECUTE FUNCTION revoke_hack_notifications();

-- Team finding (SIM-225).
ALTER TABLE events ADD COLUMN IF NOT EXISTS team_finding boolean NOT NULL DEFAULT true;
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS open boolean NOT NULL DEFAULT false;
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS looking_for text NOT NULL DEFAULT '';
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS looking_intro text NOT NULL DEFAULT '';
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS looking boolean NOT NULL DEFAULT false;

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
-- real, nameable countries or canonical -- the API's checkHackCountry is the
-- actual gate (security review Grok 9); this only stops garbage from a
-- direct insert.
DO $$ BEGIN
  ALTER TABLE event_members ADD CONSTRAINT event_members_country_chk CHECK (country = '' OR country ~ '^[A-Z]{2}$');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
-- The per-account and per-event pending caps (too_many_pending,
-- event_applications_full) count joined_via='application' AND
-- approval_status='pending' rows; these are what those counts scan.
CREATE INDEX IF NOT EXISTS event_members_pending_app_user_idx ON event_members (user_id)
  WHERE joined_via = 'application' AND approval_status = 'pending';
CREATE INDEX IF NOT EXISTS event_members_pending_app_event_idx ON event_members (event_id)
  WHERE joined_via = 'application' AND approval_status = 'pending';

CREATE TABLE IF NOT EXISTS event_team_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  team_id uuid NOT NULL REFERENCES event_teams(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('request','invite')),
  created_by uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (team_id, user_id)
);
-- Leave this event (SIM-340, 2026-10-31). A judge, or an organiser who judged, who leaves
-- keeps the scores that counted when they left: counted_team_ids is that set, frozen, so
-- later assignment or panel changes don't move them. Conflicts still apply.
CREATE TABLE IF NOT EXISTS event_former_judges (
  event_id         UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  display_name     TEXT NOT NULL DEFAULT '', -- unused since 2026-11-06 (one name per person: users.display_name)
  role             TEXT NOT NULL CHECK (role IN ('judge','organiser')),
  counted_team_ids UUID[] NOT NULL DEFAULT '{}',
  left_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id, user_id)
);

-- Joining again in any role ends the record: a returning judge counts as a member again,
-- and someone who comes back as a participant must not keep counted scores. Rejoining as
-- a participant also deletes their event_scores rows outright (Grok security review,
-- 2026-10-31): a judge who left, rejoined as a participant, and could later be given the
-- judge link again should not have their old rows quietly start counting again.
CREATE OR REPLACE FUNCTION clear_hack_former_judge() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM event_former_judges WHERE event_id = NEW.event_id AND user_id = NEW.user_id;
 IF NEW.role = 'participant' THEN
   DELETE FROM event_scores WHERE event_id = NEW.event_id AND judge_id = NEW.user_id;
 END IF;
 RETURN NULL;
END;
$$;
CREATE OR REPLACE TRIGGER hack_members_clear_former_judge
 AFTER INSERT ON event_members
 FOR EACH ROW EXECUTE FUNCTION clear_hack_former_judge();

-- Leave this event, widened (SIM-340, owner decision 2026-10-31): past a team's
-- deadline, leaving never deletes a team it empties -- the team, its entry, its
-- site and its scores stay. kept_after_leave marks exactly that: a team LeaveMembership
-- kept instead of deleting because a participant leaving the event (never a team-only
-- move, and never an organiser's Remove person) emptied it while it was not
-- TeamKeepableSQL. ListTeamsWithMembers reads this flag instead of including every
-- memberless frozen team, so a team an organiser emptied with Remove person before this
-- feature shipped -- already correctly excluded from judging, results and dashboards --
-- stays excluded; only a team kept by this feature's own write path reappears.
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS kept_after_leave boolean NOT NULL DEFAULT false;

-- Teams redesign (owner 2026-10-31): team admins, locking, team size 20
-- (host/db/migrations/zt-hack-teams-redesign.sql backfills an existing database;
-- a fresh install has no rows to backfill).
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS team_admin boolean NOT NULL DEFAULT false;
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS team_joined_at timestamptz;
ALTER TABLE event_teams   ADD COLUMN IF NOT EXISTS locked_at timestamptz;
ALTER TABLE event_teams   ADD COLUMN IF NOT EXISTS locked_by uuid REFERENCES users(id) ON DELETE SET NULL;
DO $$ BEGIN
  ALTER TABLE event_members ADD CONSTRAINT event_members_team_admin_chk
    CHECK (NOT team_admin OR team_id IS NOT NULL);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
-- Backstop for every way team_id can change that is not setMemberTeam's own
-- UPDATE: event_teams' ON DELETE SET NULL (deleting a team is an UPDATE on
-- its members under the hood), any direct SQL that only touches team_id,
-- and an old binary's writer during a deploy. See
-- products/hack/db/migrations/zt-hack-teams-redesign.sql for the incidents this
-- backstops (deleting a team its sole member admins; a move between teams
-- that does not also clear admin and tenure itself). team_admin always
-- goes false on any change; team_joined_at only goes to NULL when the
-- statement left it unchanged from OLD, so a statement that set a fresh
-- value itself (setMemberTeam always does) is left alone.
CREATE OR REPLACE FUNCTION hack_clear_team_admin_on_team_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.team_admin := false;
  IF NEW.team_joined_at IS NOT DISTINCT FROM OLD.team_joined_at THEN
    NEW.team_joined_at := NULL;
  END IF;
  RETURN NEW;
END;
$$;
CREATE OR REPLACE TRIGGER event_members_team_admin_follows_team
 BEFORE UPDATE ON event_members
 FOR EACH ROW WHEN (NEW.team_id IS DISTINCT FROM OLD.team_id)
 EXECUTE FUNCTION hack_clear_team_admin_on_team_change();
-- event_teams.open ("who can join") is no longer read. Dropped by a later
-- migration once this build has been live a few days.
COMMENT ON COLUMN event_teams.open IS 'Deprecated 2026-10-31, unused. Drop after the teams redesign settles.';

-- Notify people when something waits on them (2026-11-02, audit item 7;
-- mirrors host/db/migrations/zw-hack-wait-notices.sql).
-- One row per person per thing that happened: a join request for a team's
-- admins, an invite, an application for the organisers, a decision, a site
-- take-down or restore, a deadline change, being made team admin, a team
-- lock, a removal. The row is both the in-app news line (seen_at) and the
-- email queue (email_state). team_id and event_id have no foreign key on
-- purpose: a removal notice outlives the team it names, and an insert, often
-- made while member or team rows are already locked, must not take a
-- key-share lock on an event or team row out of the event, member, team lock
-- order (a stage change holds the event FOR UPDATE and then waits on those
-- rows). DeleteEvent removes an event's notices itself, and the email sweep
-- skips and prunes any whose event is gone.
CREATE TABLE IF NOT EXISTS event_notices (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id     UUID NOT NULL,
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL CHECK (kind IN ('join_request','invite','request_answered','application_new',
                 'application_decided','site_taken_down','site_restored','deadline_changed','team_admin',
                 'team_locked','team_removed','entry_reminder',
                 -- 0003-hack-notices-judges-messages.sql
                 'judging_open','judging_reminder','team_message',
                 -- 0015-hack-team-to-organiser-messages.sql
                 'team_inbound')),
  team_id      UUID,
  ref_id       UUID,
  detail       TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  seen_at      TIMESTAMPTZ,
  -- none: never emailed (no address, opted out, or an in-app-only kind);
  -- pending: waits for the sweep; sent; skipped: stale or opted out at send time.
  email_state  TEXT NOT NULL DEFAULT 'none' CHECK (email_state IN ('none','pending','sent','skipped')),
  emailed_at   TIMESTAMPTZ,
  attempted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS event_notices_user_event_idx ON event_notices (user_id, event_id, created_at DESC);
CREATE INDEX IF NOT EXISTS event_notices_pending_idx ON event_notices (created_at) WHERE email_state = 'pending';
CREATE INDEX IF NOT EXISTS event_notices_sent_idx ON event_notices (user_id, kind, emailed_at) WHERE email_state = 'sent';
CREATE INDEX IF NOT EXISTS event_notices_team_idx ON event_notices (team_id) WHERE team_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS event_notices_created_idx ON event_notices (created_at);
CREATE INDEX IF NOT EXISTS event_notices_event_idx ON event_notices (event_id);
-- A database that ran an earlier draft of this file kept the foreign key.
ALTER TABLE event_notices DROP CONSTRAINT IF EXISTS event_notices_event_id_fkey;

-- Entry deadline reminders (zx-hack-entry-reminders.sql): one row per team per
-- reminder (24h, 1h), for the effective deadline it was sent for.
CREATE TABLE IF NOT EXISTS event_entry_reminders (
  event_id  UUID NOT NULL,
  team_id   UUID NOT NULL REFERENCES event_teams(id) ON DELETE CASCADE,
  -- 0003-hack-notices-judges-messages.sql: any offset label (was a fixed
  -- ('24h','1h') pair), e.g. '72h', '90m'.
  kind      TEXT NOT NULL CHECK (kind ~ '^[0-9]+[hm]$'),
  deadline  TIMESTAMPTZ NOT NULL,
  sent_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id, team_id, kind)
);
CREATE INDEX IF NOT EXISTS event_entry_reminders_team_idx ON event_entry_reminders (team_id);
-- "Email me when something waits on me" on Account. On unless turned off.
ALTER TABLE hack_account_preferences ADD COLUMN IF NOT EXISTS email_updates BOOLEAN NOT NULL DEFAULT TRUE;

-- Judging-close reminders (0003-hack-notices-judges-messages.sql): one row
-- per (judge, offset), re-armed when judging_ends_at moves.
CREATE TABLE IF NOT EXISTS event_judge_reminders (
  event_id  UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind      TEXT NOT NULL CHECK (kind ~ '^[0-9]+[hm]$'),
  deadline  TIMESTAMPTZ NOT NULL,
  sent_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id, user_id, kind)
);

-- Organiser team messages (0003-hack-notices-judges-messages.sql): the
-- durable history. Delivery is the notice (kind team_message); notices are
-- pruned after 30 days and exist only per recipient.
CREATE TABLE IF NOT EXISTS event_team_messages (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id    UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  team_id     UUID NOT NULL REFERENCES event_teams(id) ON DELETE CASCADE,
  sender_id   UUID REFERENCES users(id) ON DELETE SET NULL,
  sender_name TEXT NOT NULL DEFAULT '', -- unused since 2026-11-21 (names come from the account by sender_id)
  body        TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- 0015: true when a member of the team wrote it to the organisers
  from_team   BOOLEAN NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS event_team_messages_team_idx ON event_team_messages (team_id, created_at DESC);
CREATE INDEX IF NOT EXISTS event_team_messages_event_idx ON event_team_messages (event_id, created_at);

-- Entries and participant journey, slice 1 (host/db/migrations/zv-hack-entry-summary.sql):
-- the summary card, explicit submit against per-event rules, and the settle
-- sweep. entry_required stays, kept in sync, for one release (the deprecated
-- PATCH .../events/{slug} entry_required key and older readers).
ALTER TABLE event_entries ADD COLUMN IF NOT EXISTS problem      text    NOT NULL DEFAULT '';
ALTER TABLE event_entries ADD COLUMN IF NOT EXISTS audience     text    NOT NULL DEFAULT '';
ALTER TABLE event_entries ADD COLUMN IF NOT EXISTS tools        text[]  NOT NULL DEFAULT '{}';
ALTER TABLE event_entries ADD COLUMN IF NOT EXISTS maturity     text    NOT NULL DEFAULT ''
      CHECK (maturity IN ('', 'working', 'partial', 'concept'));
ALTER TABLE event_entries ADD COLUMN IF NOT EXISTS submitted_at timestamptz;
ALTER TABLE event_entries ADD COLUMN IF NOT EXISTS submitted_by uuid REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE event_entries ADD COLUMN IF NOT EXISTS submit_auto  boolean NOT NULL DEFAULT false;
-- The deadline sweep has decided this entry (submitted it, or left it not
-- submitted): reset by an extension, a stage move back to building, or the
-- event deadline moving later.
ALTER TABLE event_entries ADD COLUMN IF NOT EXISTS submit_settled_at timestamptz;
-- Coordinator decision, 2026-11-02 (INTENT.md, owner may overrule): an
-- incomplete draft settled as "not submitted" is still judged, carrying an
-- Incomplete flag, rather than dropped from judging. excluded_at/by is the
-- organiser's hand exclude of a flagged entry from the Entries screen (with
-- a confirm) -- the only way such an entry stops being judged.
ALTER TABLE event_entries ADD COLUMN IF NOT EXISTS excluded_at timestamptz;
ALTER TABLE event_entries ADD COLUMN IF NOT EXISTS excluded_by uuid REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS event_entries_unsettled_idx
  ON event_entries (event_id) WHERE submitted_at IS NULL AND submit_settled_at IS NULL;

ALTER TABLE events ADD COLUMN IF NOT EXISTS entry_preset text NOT NULL DEFAULT 'custom'
      CHECK (entry_preset IN ('build', 'code', 'ideas', 'custom', 'video_or_website', 'video_website_code'));
ALTER TABLE events ADD COLUMN IF NOT EXISTS entry_rules jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE events ADD COLUMN IF NOT EXISTS judge_first text NOT NULL DEFAULT 'video'
      CHECK (judge_first IN ('video', 'site'));
-- A fresh install has no existing events to backfill; zv-hack-entry-summary.sql
-- backfills entry_rules from entry_required on an existing database.

-- 0005-hack-entry-rules-chosen.sql (SIM-382): whether an organiser has ever
-- saved entry rules. DEFAULT true means a fresh install's rows (and every
-- pre-existing event on a live database) count as already chosen; the
-- create handler inserts a new event with this explicitly false.
ALTER TABLE events ADD COLUMN IF NOT EXISTS entry_rules_chosen boolean NOT NULL DEFAULT true;

CREATE TABLE IF NOT EXISTS paper_hack_key (
 event_id UUID PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
 event_key BIGINT UNIQUE NOT NULL, synthetic BOOLEAN NOT NULL,
 run_id BIGINT NOT NULL REFERENCES paper_run(run_id), first_open_at TIMESTAMPTZ
);
-- No source identifiers in the exported tables below.
CREATE TABLE IF NOT EXISTS paper_hack_event (
  snapshot_day DATE NOT NULL,
  event_key BIGINT NOT NULL,
  run_id BIGINT NOT NULL REFERENCES paper_run(run_id),
  synthetic BOOLEAN NOT NULL,
  stage TEXT NOT NULL CHECK (stage IN ('draft','open','building','closed','judging','results','archived')),
  created_week DATE NOT NULL,
  duration_hours DOUBLE PRECISION,
  expected_participants INTEGER,
  participants BIGINT,
  organisers BIGINT,
  judges BIGINT,
  applications BIGINT,
  applications_approved BIGINT,
  teams BIGINT,
  teams_with_entry BIGINT,
  teams_kept_after_leave BIGINT,
  team_size_p50 BIGINT,
  team_size_max BIGINT,
  team_sites BIGINT,
  team_site_bytes_sum BIGINT,
  team_site_bytes_p50 BIGINT,
  team_site_versions_sum BIGINT,
  event_storage_bytes BIGINT,
  submissions BIGINT,
  entries_with_code_url BIGINT,
  entries_with_video BIGINT,
  scores BIGINT,
  score_criteria BIGINT,
  assignments BIGINT,
  conflicts BIGINT,
  votes BIGINT,
  announcements BIGINT,
  results_published BOOLEAN,
  deploys_total BIGINT,
  minutes_create_to_first_deploy BIGINT,
  PRIMARY KEY(snapshot_day,event_key)
);

CREATE INDEX IF NOT EXISTS paper_hack_event_key_day_idx ON paper_hack_event(event_key,snapshot_day DESC);

-- Active event-minute summaries. Histograms are separate to avoid repeating
-- and accidentally summing event-wide member/concurrency/arrival totals.
CREATE TABLE IF NOT EXISTS paper_hack_minute (
 run_id BIGINT NOT NULL REFERENCES paper_run(run_id), slot TIMESTAMPTZ NOT NULL,
 slot_secs SMALLINT NOT NULL DEFAULT 60 CHECK(slot_secs=60), event_key BIGINT NOT NULL,
 synthetic BOOLEAN NOT NULL, requests BIGINT NOT NULL, writes BIGINT NOT NULL, data_writes BIGINT NOT NULL,
 deploys BIGINT NOT NULL, active_members INTEGER NOT NULL, inflight_max BIGINT NOT NULL,
 peak_rps_1s BIGINT NOT NULL, arrivals_1s INT4[] NOT NULL CHECK(cardinality(arrivals_1s)=60),
 writes_1s INT4[] NOT NULL CHECK(cardinality(writes_1s)=60),
 PRIMARY KEY(slot,event_key,run_id)
);
CREATE TABLE IF NOT EXISTS paper_hack_latency (
 run_id BIGINT NOT NULL REFERENCES paper_run(run_id), slot TIMESTAMPTZ NOT NULL,
 event_key BIGINT NOT NULL, synthetic BOOLEAN NOT NULL,
 class TEXT NOT NULL CHECK(class IN ('site_file','data_read','data_write','auth','mcp','api_read','api_write','platform_page')),
 status_group TEXT NOT NULL CHECK(status_group IN ('2xx','3xx','4xx','5xx')),
 n BIGINT NOT NULL, sum_us BIGINT NOT NULL, max_us BIGINT NOT NULL, bytes_out BIGINT NOT NULL,
 idx_offset SMALLINT NOT NULL, counts INT8[] NOT NULL,
 arrivals_1s INT4[] NOT NULL CHECK(cardinality(arrivals_1s)=60),
 writes_1s INT4[] NOT NULL CHECK(cardinality(writes_1s)=60),
 PRIMARY KEY(slot,event_key,class,status_group,run_id)
);
CREATE TABLE IF NOT EXISTS paper_hack_phase (
 run_id BIGINT NOT NULL REFERENCES paper_run(run_id), event_key BIGINT NOT NULL, synthetic BOOLEAN NOT NULL,
 stage TEXT NOT NULL CHECK(stage IN ('draft','open','building','closed','judging','results','archived','submission_deadline','judging_locked','results_published','voting_opens','voting_closes')),
 phase_slot TIMESTAMPTZ NOT NULL, minute_offset INTEGER, origin_slot TIMESTAMPTZ, observed BOOLEAN NOT NULL, baseline BOOLEAN NOT NULL DEFAULT false,
 recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), origin_change TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(event_key,stage,origin_change)
);
-- Versioned companion for Hack metrics in the legacy shared paper_daily table.
CREATE TABLE IF NOT EXISTS paper_hack_daily (
 run_id BIGINT NOT NULL REFERENCES paper_run(run_id), day DATE NOT NULL,
 metric TEXT NOT NULL, value DOUBLE PRECISION NOT NULL,
 PRIMARY KEY(day,metric)
);

-- SIM-393: shared optional-item marks and explicit rubric/conflict acknowledgements.
CREATE TABLE IF NOT EXISTS event_checklist_dismissals (
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    item_id text NOT NULL,
    marked_by uuid REFERENCES users(id) ON DELETE SET NULL,
    marked_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, item_id)
);
CREATE TABLE IF NOT EXISTS event_checklist_acknowledgements (
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    subject_id text NOT NULL,
    item_id text NOT NULL,
    version text NOT NULL DEFAULT '',
    acknowledged_by uuid REFERENCES users(id) ON DELETE SET NULL,
    acknowledged_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, subject_id, item_id)
);

-- 0011-hack-event-theme.sql (SIM-353): whole-page style for the public
-- event page; '' is Classic (today's look).
ALTER TABLE events ADD COLUMN IF NOT EXISTS theme text NOT NULL DEFAULT ''
      CHECK (theme IN ('', 'poster', 'confetti', 'boardroom', 'garden', 'terminal'));

-- SIM-394: host/db/migrations/0012-hack-assistant-usage.sql.
CREATE TABLE IF NOT EXISTS hack_assistant_usage (
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day       DATE NOT NULL,
    questions INT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);
CREATE TABLE IF NOT EXISTS hack_assistant_spend (
    month         DATE PRIMARY KEY,
    cost_micro_usd BIGINT NOT NULL DEFAULT 0
);

