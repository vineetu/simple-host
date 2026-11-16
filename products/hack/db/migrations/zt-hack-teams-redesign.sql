-- Teams redesign (owner 2026-10-31): team admins, locking, team size 20.
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS team_admin boolean NOT NULL DEFAULT false;
ALTER TABLE event_members ADD COLUMN IF NOT EXISTS team_joined_at timestamptz;
ALTER TABLE event_teams   ADD COLUMN IF NOT EXISTS locked_at timestamptz;
ALTER TABLE event_teams   ADD COLUMN IF NOT EXISTS locked_by uuid REFERENCES users(id) ON DELETE SET NULL;

-- Tenure for "longest-standing member": best available is the event join time.
UPDATE event_members SET team_joined_at = joined_at
 WHERE team_id IS NOT NULL AND team_joined_at IS NULL;

-- Admin: the creator when still on the team ...
UPDATE event_members m SET team_admin = true
  FROM event_teams t
 WHERE m.team_id = t.id AND m.user_id = t.created_by;
-- ... otherwise the member who joined first.
UPDATE event_members m SET team_admin = true
 WHERE m.team_id IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM event_members a WHERE a.team_id = m.team_id AND a.team_admin)
   AND m.user_id = (SELECT o.user_id FROM event_members o WHERE o.team_id = m.team_id
                     ORDER BY o.team_joined_at, o.user_id LIMIT 1);

DO $$ BEGIN
  ALTER TABLE event_members ADD CONSTRAINT event_members_team_admin_chk
    CHECK (NOT team_admin OR team_id IS NOT NULL);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- Every Go write path that takes a member off a team, or moves them to a
-- different one, clears team_admin and sets a fresh team_joined_at (or
-- clears it too, when leaving) in the same statement as team_id
-- (setMemberTeam). This trigger is the backstop for every way team_id can
-- change that is not that one Go statement: event_members.team_id's own ON
-- DELETE SET NULL (deleting a team deletes its row, which Postgres
-- implements as an UPDATE on every referencing member row), any direct SQL
-- that only touches team_id, and an old binary's writer running during a
-- deploy. Without the NULL case, deleting a team its sole member admins
-- fails event_members_team_admin_chk outright, rather than just losing
-- admin; without the changed-team case, a write that moves team_id to a
-- different team without also clearing admin and tenure itself would carry
-- both of those stale across the move (Grok security review, 2026-11-01).
-- team_admin always goes false on any team_id change: no current or future
-- Go path grants admin in the same statement as a team_id change, so this
-- never fights one. team_joined_at only goes to NULL when the statement
-- left it unchanged from OLD (comparing NEW to OLD, not team_id's own
-- change, is what makes this safe): setMemberTeam always sets a fresh
-- clock_timestamp() (or NULL, leaving) in the very same UPDATE, so NEW is
-- never left equal to OLD there, and this trigger defers to it.
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

-- A team kept after its last member left can never take anyone again: locked for good.
UPDATE event_teams SET locked_at = COALESCE(locked_at, now()) WHERE kept_after_leave;

-- Team size: 20 by default, and for every event below it.
ALTER TABLE events ALTER COLUMN team_size_max SET DEFAULT 20;
UPDATE events SET team_size_max = 20 WHERE team_size_max < 20;

-- event_teams.open ("who can join") is no longer read. Dropped by a later migration once this
-- build has been live a few days, so a rollback to the previous binary keeps working.
COMMENT ON COLUMN event_teams.open IS 'Deprecated 2026-10-31, unused. Drop after the teams redesign settles.';
