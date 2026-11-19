-- Teams (TM-S1, owner decisions 2026-11-19): an account that owns sites and
-- that people join. The team is a users row with is_team set; members act
-- with their own sign-in, key or connection. People join only by accepting
-- an invitation (team_invitations). Invitations, membership changes and
-- team creation and deletion are recorded in team_audit.
--
-- Additive and idempotent: a column with a constant default (no table
-- rewrite), three new tables and three guard triggers. Safe while the previous
-- program is running: it never sets is_team, so the guards never fire for it.
-- Apply before deploying the build that reads these (it refuses to start
-- without them).

ALTER TABLE users ADD COLUMN IF NOT EXISTS is_team BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS team_members (
  team_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  user_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  added_by UUID REFERENCES users(id) ON DELETE SET NULL,
  added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (team_id, user_id)
);
CREATE INDEX IF NOT EXISTS team_members_user_idx ON team_members (user_id);

CREATE TABLE IF NOT EXISTS team_audit (
  id          BIGSERIAL PRIMARY KEY,
  team_id     UUID NOT NULL,
  team_handle TEXT NOT NULL,
  actor_id    UUID REFERENCES users(id) ON DELETE SET NULL,
  action      TEXT NOT NULL CHECK (action IN ('create', 'delete', 'remove_member', 'invite', 'cancel', 'accept', 'decline')),
  subject_id  UUID REFERENCES users(id) ON DELETE SET NULL,
  invitation_id UUID,
  at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS team_audit_team_idx ON team_audit (team_id, at);

-- An invitation to join a team: nobody becomes a member without accepting.
-- addressed_to is an email in its sign-in form (trimmed, lower case). No
-- account is looked up when it is made; it is offered to the signed-in
-- person whose verified sign-in email it is. Pending ones expire, and go
-- when the inviter leaves the team.
CREATE TABLE IF NOT EXISTS team_invitations (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  team_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  invited_by   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  addressed_to TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL,
  state        TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'accepted', 'declined', 'cancelled'))
);
CREATE UNIQUE INDEX IF NOT EXISTS team_invitations_pending ON team_invitations (team_id, addressed_to) WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS team_invitations_to_idx ON team_invitations (addressed_to) WHERE state = 'pending';

-- A team never holds an API key.
CREATE OR REPLACE FUNCTION sh_team_no_keys() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_team) THEN
    RAISE EXCEPTION 'a team never holds an API key' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS api_keys_team_no_keys ON api_keys;
CREATE TRIGGER api_keys_team_no_keys BEFORE INSERT OR UPDATE OF user_id ON api_keys
  FOR EACH ROW EXECUTE FUNCTION sh_team_no_keys();

-- A membership row links a team to a person.
CREATE OR REPLACE FUNCTION sh_team_member_kinds() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM users WHERE id = NEW.team_id AND is_team) THEN
    RAISE EXCEPTION 'team_members.team_id must be a team' USING ERRCODE = 'check_violation';
  END IF;
  IF EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_team) THEN
    RAISE EXCEPTION 'team_members.user_id must be a person' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS team_members_kinds ON team_members;
CREATE TRIGGER team_members_kinds BEFORE INSERT OR UPDATE ON team_members
  FOR EACH ROW EXECUTE FUNCTION sh_team_member_kinds();

-- An account's kind cannot change while membership rows point at it, and an
-- account that holds a key cannot become a team.
CREATE OR REPLACE FUNCTION sh_team_kind_fixed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.is_team IS DISTINCT FROM OLD.is_team THEN
    IF EXISTS (SELECT 1 FROM team_members WHERE team_id = OLD.id OR user_id = OLD.id) THEN
      RAISE EXCEPTION 'an account''s kind cannot change while it has team memberships' USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.is_team AND EXISTS (SELECT 1 FROM api_keys WHERE user_id = OLD.id) THEN
      RAISE EXCEPTION 'a team never holds an API key' USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS users_team_kind_fixed ON users;
CREATE TRIGGER users_team_kind_fixed BEFORE UPDATE OF is_team ON users
  FOR EACH ROW EXECUTE FUNCTION sh_team_kind_fixed();
