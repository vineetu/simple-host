-- simple-hack.app hosted hackathons, milestone 3: judging (rubric,
-- assignment, conflicts, scores, lock, publish, results)
-- (docs/designs/simple-hack-platform.md). Used only by an instance running
-- EVENTS=hosted; additive and idempotent.

-- Rubric: ordered criteria the organiser sets before judging opens. Weights
-- are percent and must sum to 100 (checked in Go, not SQL).
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

-- Judging settings per event: open (every judge scores every team) or
-- automatic (balanced assignment); judges_per_team is automatic mode's
-- target. M3 ties are flagged and the organiser picks manually at publish
-- time (no automatic tie-break rule yet; that is P1), so there is no
-- tie-break-criterion column here.
ALTER TABLE events ADD COLUMN IF NOT EXISTS judge_assignment_mode TEXT NOT NULL DEFAULT 'open';
ALTER TABLE events ADD COLUMN IF NOT EXISTS judges_per_team INTEGER NOT NULL DEFAULT 2;
ALTER TABLE events ADD COLUMN IF NOT EXISTS judging_locked_at TIMESTAMPTZ;
ALTER TABLE events ADD COLUMN IF NOT EXISTS judging_lock_reason TEXT NOT NULL DEFAULT '';

-- Judge <-> team assignment. In "open" mode no rows are needed (every judge
-- may score every team); "automatic" mode fills this table with a balanced
-- spread. A row here is what the judge's queue is built from in automatic
-- mode.
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

-- A judge's conflict with a team: excluded from assignment and totals.
-- declared_by records who created the row ('judge' self-declare, 'organiser'
-- marked); either an organiser or the judge themself may remove it.
CREATE TABLE IF NOT EXISTS event_conflicts (
  event_id     UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  judge_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  team_id      UUID NOT NULL REFERENCES event_teams(id) ON DELETE CASCADE,
  declared_by  TEXT NOT NULL DEFAULT 'judge',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id, judge_id, team_id)
);

-- One judge's score for one team on one criterion. Upserted as a whole set
-- per (judge, team) write (the scoring screen saves every criterion row at
-- once). comment is one free-text note per (judge, team), stored once per
-- criterion row for simplicity (same value in every row of that pair) OR
-- kept on the first row only -- the Go layer decides; the column exists on
-- every row so either approach reads back the same way.
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

-- Published results snapshot: computed once at publish time so the public
-- page and each team's private view never recompute live over judges' raw
-- scores. One row per event; publishing again overwrites it. full_ranking
-- is a display switch the organiser can flip at any time without
-- recomputing (owner decision: public sees winners only by default, the
-- organiser can switch to a full public ranking at any time).
CREATE TABLE IF NOT EXISTS event_results (
  event_id      UUID PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
  published_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  full_ranking  BOOLEAN NOT NULL DEFAULT FALSE,
  snapshot      JSONB NOT NULL
);
