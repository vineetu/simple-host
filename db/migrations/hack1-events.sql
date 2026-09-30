-- simple-hack.app hosted hackathons, milestone 1: events, members and teams
-- (docs/designs/simple-hack-platform.md). Used only by an instance running
-- EVENTS=hosted; on any other instance the tables stay empty, and the server
-- checks them at start only in hosted mode (internal/db/schemacheck.go).
--
-- An event is held by an internal account whose handle is the event's name
-- (events.account_id), so <event>.<SITE_DOMAIN> is that account's address and,
-- from M2, each team's site is a site of that account named after the team.
--
-- Fields for later milestones are here already so the model does not change
-- shape under running events: the submission deadline (M2), results
-- visibility and publishing (M3), and the retention clock (M4).
--
-- Additive and idempotent: new tables only.

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
  about                 TEXT NOT NULL DEFAULT '',
  rules                 TEXT NOT NULL DEFAULT '',
  prizes                TEXT NOT NULL DEFAULT '',
  coc_text              TEXT NOT NULL DEFAULT '',
  time_zone             TEXT NOT NULL DEFAULT 'UTC',
  starts_at             TIMESTAMPTZ,
  ends_at               TIMESTAMPTZ,
  -- Settings.
  team_size_max         INTEGER NOT NULL DEFAULT 4 CHECK (team_size_max BETWEEN 1 AND 50),
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
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
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

-- One row per person per event: one role each (an organiser is not also a
-- participant, a judge is never on a team). A participant is on at most one
-- team of the event (team_id).
CREATE TABLE IF NOT EXISTS event_members (
  event_id        UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role            TEXT NOT NULL CHECK (role IN ('organiser','participant','judge')),
  display_name    TEXT NOT NULL DEFAULT '',
  team_id         UUID REFERENCES event_teams(id) ON DELETE SET NULL,
  coc_accepted_at TIMESTAMPTZ,
  joined_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id, user_id),
  CHECK (team_id IS NULL OR role = 'participant')
);
CREATE INDEX IF NOT EXISTS event_members_user_idx ON event_members (user_id);
CREATE INDEX IF NOT EXISTS event_members_team_idx ON event_members (team_id);

-- Every event creation, kept when the event is deleted, so deleting drafts
-- never resets EVENT_CREATE_PER_DAY.
CREATE TABLE IF NOT EXISTS event_create_log (
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS event_create_log_user_idx ON event_create_log (user_id, created_at);
